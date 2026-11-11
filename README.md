# pi@home

Services running on the home Raspberry Pi. Each service lives in its own directory with its own `compose.yml`, managed independently — there is no root compose file tying them together.

## Conventions

- **File naming**: `compose.yml` (not `docker-compose.yml`) inside each service directory.
- **Persistent state**: any service with runtime state to persist bind-mounts a single flat `./data` directory into whatever path the container expects (e.g. `./data:/etc/pihole`, `./data:/mosquitto/data`). One `data/` per service, no nested subfolders — keeps every service's compose file predictable to read and the blanket `data/` rule in `.gitignore` trivially covers all of them. Static, version-controlled config (not runtime state) lives outside `data/` instead — e.g. `mosquitto/config/mosquitto.conf`, `caddy/Caddyfile`.
- **Env vars, secrets, and deployment-specific values**: config that's the same regardless of which Pi/network this runs on (`TZ`, ports, intervals) lives directly in `compose.yml` under `environment:`. A service gets a `.env` file when a value is either a secret (e.g. `PIHOLE_PASSWORD`) or genuinely deployment-specific — would differ if this repo were deployed elsewhere (e.g. `PIHOLE_LAN_IP`) — referenced via `${VAR}` interpolation either way. `.env`/`*.env` is blanket-gitignored so both cases are covered automatically.
- `container_name` is always set explicitly (matches the service name) and `restart: unless-stopped` is always used.
- **Log size**: every service caps its logs (`logging: driver: json-file, options: {max-size: "10m", max-file: "3"}`, ~30MB max per container) — Docker's `json-file` driver has no size limit by default, and this is an SD card, not a server disk.

## Services

- `pihole/` — DNS ad-blocking. Web UI is not published to the host — reachable only via Caddy.
- `mosquitto/` — MQTT broker used as the pub/sub backbone for sensors and other services.
- `dht22/` — reads the DHT22 sensor in the basement low-voltage cabinet (wired to GPIO4) roughly every 15s, and every 2 minutes averages that window's readings and publishes temperature, humidity, and a calculated dew point to Mosquitto on `home/dht22/{temperature,humidity,dew_point}` (retained).
- `caddy/` — reverse proxy. The only service bound to host ports 80/443. Routes friendly hostnames under `.lan` to each service's web UI, with HTTPS via Caddy's own internal CA.
- `homebridge/` — HomeKit bridge, exposing the DHT22 readings (via MQTT) as Apple Home accessories. Runs with `network_mode: host` — see below, it's a deliberate exception to how every other service is networked.
- `scrypted/` — exposes the UniFi Protect doorbell to Apple Home (live view, two-way talk, ring notifications, HomeKit Secure Video). Also `network_mode: host`, for the same reason as Homebridge — see "Scrypted" below.

## Accessing services by hostname

Each service with a web UI gets a subdomain under `.lan` (e.g. `https://pihole.lan`), proxied by Caddy. New sites are added as blocks in `caddy/Caddyfile`.

`.lan` is not the most "spec-correct" choice — `.local` is reserved for mDNS/Bonjour (intercepted by macOS/iOS/Linux/Windows before a real DNS query fires) and `home.arpa` is the IETF-standardized domain for exactly this (RFC 8375) — but we tried `home.arpa` first and hit a confirmed bug in Pi-hole FTL (v6.7): FTL hardcodes RFC 8375 compliance for `home.arpa`, always answering it locally and refusing to forward it, and the documented escape hatch for this (`dns.domain.name`/`dns.domain.local`, added in FTL PR #2772) did not work even when verified correctly applied in FTL's own live config. `.lan` sidesteps the whole problem: it's Pi-hole's own **default** local domain (`dns.domain.name` defaults to `"lan"`, `dns.domain.local` defaults to `true`), so there's no hardcoded special-case behavior fighting a plain wildcard override.

### One-time setup: wildcard DNS in Pi-hole

Pi-hole needs to answer for `*.lan` and point it at the Pi. This is declared in `pihole/compose.yml` (`FTLCONF_misc_dnsmasq_lines: address=/lan/${PIHOLE_LAN_IP}`) — the only thing to do is set the Pi's actual LAN IP once in `pihole/.env`:

```
PIHOLE_LAN_IP=<pi-lan-ip>
```

(`dns.domain.name`/`dns.domain.local` are deliberately left unset — `.lan` is already Pi-hole's default for both, see above.)

After changing `pihole/.env`, recreate the container so the new env var is picked up: `docker compose -f pihole/compose.yml up -d`. Verify with `dig @<pi-lan-ip> pihole.lan` — it should return `<pi-lan-ip>` in the answer section, not `NXDOMAIN`.

This only works for devices that already use Pi-hole as their DNS resolver (should be true network-wide already). Give the Pi a DHCP reservation on your router so `<pi-lan-ip>` doesn't drift — if it ever does, update `PIHOLE_LAN_IP` and recreate the container again.

### One-time setup: trusting Caddy's internal CA

Caddy issues certificates from its own internal CA rather than a public one (there's no way to get a publicly-trusted cert for a made-up local domain). Each client device needs to trust that CA once, or you'll get certificate warnings:

1. Extract the root cert from the Pi: `docker cp caddy:/data/caddy/pki/authorities/local/root.crt ./caddy-root.crt`
2. **macOS**: `security add-trusted-cert -d -r trustRoot -k ~/Library/Keychains/login.keychain-db caddy-root.crt` (or double-click the file and set it to "Always Trust" in Keychain Access).
3. **iOS**: AirDrop or email the file to the device, install the profile (Settings → Profile Downloaded), then separately enable full trust under Settings → General → About → Certificate Trust Settings — installing the profile alone isn't enough as of iOS 10.3+.

`caddy/data/` persists the CA root across restarts — don't delete it, or every device will need to re-trust a new one.

## Shared network

Since each service is its own Compose project, services can't reach each other by name by default. Services that need to talk to one another join a shared external Docker network, created once per Pi:

```bash
docker network create --ipv6 homelab
```

Running this a second time doesn't create a duplicate or break anything — Docker just refuses with `Error response from daemon: network with name homelab already exists` and exits non-zero. Safe to ignore, or make it idempotent:

```bash
docker network inspect homelab >/dev/null 2>&1 || docker network create --ipv6 homelab
```

Any compose file that needs to reach another service declares:

```yaml
networks:
  homelab:
    external: true
```

`pihole`, `mosquitto`, `dht22`, and `caddy` join it today — Pi-hole needs it so Caddy can reach its web UI by container name. `homebridge` is the one exception: see below.

**Why `--ipv6`:** the LAN runs dual-stack and the router advertises the Pi's IPv6 address as a DNS server, so many clients query Pi-hole over IPv6. On an IPv4-only bridge network, Docker can only forward those through its userland proxy, which re-originates each query from the network gateway (`172.18.0.1`) — Pi-hole still filters them, but every IPv6 client collapses into that one address in its stats and per-client group rules. With IPv6 enabled on the network, Docker (27+, where `ip6tables` is on by default) forwards IPv6 with real DNAT rules instead, so Pi-hole sees each client's actual address. Docker assigns the network a random ULA `/64` on its own; no `daemon.json` changes are needed.

An existing network can't have IPv6 switched on in place. To convert one, stop every service on it (`docker compose -f <svc>/compose.yml down` for `pihole`, `mosquitto`, `dht22`, `caddy`), then `docker network rm homelab`, `docker network create --ipv6 homelab`, and bring them back up — about a minute of DNS downtime.

## Homebridge networking

HomeKit relies on mDNS/Bonjour multicast plus dynamic per-accessory TCP ports, neither of which survive Docker's normal bridge-mode NAT. The official Homebridge image requires `network_mode: host` to work at all — so, unlike every other service here, `homebridge/compose.yml` does **not** join `homelab` (Docker Compose doesn't allow `network_mode: host` and `networks:` on the same service anyway). Two consequences fall out of that:

- **Reaching Mosquitto**: Homebridge connects to `mqtt://127.0.0.1:1883` — since it shares the host's network namespace, it hits Mosquitto's already-published host port directly, no container name needed.
- **Caddy reaching Homebridge**: Caddy is still on `homelab` (a bridge network) and can't resolve `homebridge` by container name either. Instead, `caddy/compose.yml` sets `extra_hosts: ["host.docker.internal:host-gateway"]` (a Linux Docker Engine 20.10+ feature) so its `homebridge.lan` site block can target `host.docker.internal:8581` — the host's own IP — without hardcoding the Pi's LAN address anywhere.

**mDNS advertiser**: Homebridge defaults to its own bundled "Ciao" mDNS responder, but Raspberry Pi OS already runs `avahi-daemon` (that's what serves `raspberrypi.local`), and running both on UDP 5353 has documented reliability issues. `homebridge/compose.yml` sets `ENABLE_AVAHI: "0"` and bind-mounts the host's `/var/run/dbus` and `/var/run/avahi-daemon/socket` so Homebridge can be pointed at the host's Avahi instead (set in the UI — see setup steps below).

### One-time setup: Homebridge

1. `docker compose -f homebridge/compose.yml up -d`, then visit `https://homebridge.lan` and complete the first-run admin account wizard.
2. In the UI: **Settings → Homebridge Settings → Advertiser → Avahi**, then restart Homebridge from the UI.
3. **Plugins → search "mqttthing" → install** (`homebridge-mqttthing`).
4. Add two accessories via the UI's config editor:
   ```json
   {
     "accessory": "mqttthing",
     "type": "temperatureSensor",
     "name": "Outdoor Temperature",
     "url": "mqtt://127.0.0.1:1883",
     "topics": { "getCurrentTemperature": "home/dht22/temperature" }
   },
   {
     "accessory": "mqttthing",
     "type": "humiditySensor",
     "name": "Outdoor Humidity",
     "url": "mqtt://127.0.0.1:1883",
     "topics": { "getCurrentRelativeHumidity": "home/dht22/humidity" }
   },
   {
     "accessory": "mqttthing",
     "type": "temperatureSensor",
     "name": "Cabinet Dew Point",
     "url": "mqtt://127.0.0.1:1883",
     "topics": { "getCurrentTemperature": "home/dht22/dew_point" }
   }
   ```
   HomeKit has no native "dew point" accessory type, so the dew point is exposed as a second temperature sensor — the name makes the meaning clear even though Home shows a thermometer icon.
5. Restart Homebridge, then pair it in the Apple Home app by scanning the QR/pairing code on the Homebridge UI dashboard.

## Scrypted (doorbell camera)

The doorbell is adopted in UniFi Protect on the gateway, which keeps recording to its own disk. Scrypted sits between Protect and Apple Home. It replaced a UniFi Protect plugin in Homebridge: Scrypted keeps a rolling prebuffer of the stream in RAM and hands HomeKit the camera's H.264 untouched (no transcoding), so streams open faster and two-way audio takes a shorter path. Homebridge stays for the DHT22 sensors.

Networking is the same story as Homebridge: `network_mode: host`, not on `homelab`, Caddy reaches the UI at `host.docker.internal:10443` (Scrypted's self-signed HTTPS, so the `scrypted.lan` block skips upstream certificate verification), and the host's Avahi is shared through the D-Bus/socket mounts.

Differences from Scrypted's upstream compose file, on purpose: the `:lite` image (no object-detection stack), no Watchtower auto-updates, no hardcoded public DNS servers (they would bypass Pi-hole), and no NVR volume (recording to an SD card is a bad idea; Protect and HomeKit Secure Video cover it).

### One-time setup: Scrypted

1. **Protect**: create a local-only UniFi OS user with full Protect access for Scrypted. On the doorbell, make sure the stream is H.264 (turn off Enhanced/H.265 encoding if offered) — HomeKit can't play H.265.
2. `docker compose -f scrypted/compose.yml up -d`, restart Caddy if `scrypted.lan` is new to it (`docker compose -f caddy/compose.yml restart`), then visit `https://scrypted.lan` and create the admin account.
3. **Plugins**: install **UniFi Protect** and **HomeKit** (Rebroadcast and Snapshot come along as dependencies). In the UniFi Protect plugin, enter the gateway's IP and the user from step 1; the doorbell appears as a device.
4. **Low-latency settings** on the doorbell device:
   - Rebroadcast: prebuffer enabled on the high and medium streams.
   - HomeKit stream selection: high for local, medium for remote, low for Apple Watch. Leave transcoding off.
   - HomeKit plugin → mDNS advertiser: Avahi (same choice as Homebridge). If Home has trouble finding the accessory, fall back to the default advertiser.
5. **Pair**: the doorbell is exposed as its own standalone HomeKit accessory — scan the QR code on the device's HomeKit page in Scrypted. In Home, set it to "Stream & Allow Recording" for HomeKit Secure Video (needs iCloud+ and a home hub).

### Migrating from the Homebridge plugin

Pair the Scrypted doorbell into a temporary room first so both versions exist side by side, and compare stream open time, talk delay, and ring notification speed. Then:

1. Redo notifications, automations, and chime settings on the new accessory — they don't carry over.
2. Remove the old doorbell from Home, uninstall the UniFi Protect plugin in Homebridge, and restart Homebridge.

HomeKit Secure Video clips belong to the old accessory and disappear with it; Protect's own recordings are untouched. Until step 2, rollback is just stopping the Scrypted container and removing the new accessory.

If you hear yourself when talking through Home, that is the doorbell's speaker feeding its own microphone (the Protect app cancels it; HomeKit doesn't always). Lowering the doorbell's speaker volume in Protect helps.

### Router checklist (UniFi)

HomeKit discovery is easy to break at the router/AP level. On a UniFi Cloud Gateway Fiber (or any UniFi OS gateway), assuming the Pi and your Apple devices are on one flat LAN:

- **Client Device Isolation must be off** on the WiFi network/SSID your iPhone/Mac uses (`Settings → WiFi → [SSID] → Advanced`) — this is the most common cause of "HomeKit can't find the bridge," since it blocks the device-to-device multicast traffic mDNS needs.
- **Multicast DNS (mDNS) Proxy** and **IGMP snooping** settings only matter if the Pi and your Apple devices sit on *different* VLANs — leave them alone on a flat single-LAN setup.
- **Zone-Based Firewall** (current UniFi OS default) already allows all traffic within one zone/VLAN, so no custom firewall rules should be needed unless you're running the Pi on an isolated VLAN — if so, that needs explicit rules for UDP 5353 and HAP's dynamic TCP ports, not just mDNS reflection.

## Running a service

From the repo root, every service follows the same pattern (swap in the service directory):

```bash
# start (add --build if the service has a Dockerfile, e.g. dht22)
docker compose -f <service>/compose.yml up -d

# view logs
docker compose -f <service>/compose.yml logs -f

# stop
docker compose -f <service>/compose.yml down
```

Concretely, to bring everything up on a fresh Pi:

```bash
docker network inspect homelab >/dev/null 2>&1 || docker network create --ipv6 homelab

docker compose -f pihole/compose.yml up -d
docker compose -f mosquitto/compose.yml up -d
docker compose -f dht22/compose.yml up -d --build
docker compose -f caddy/compose.yml up -d
docker compose -f homebridge/compose.yml up -d
docker compose -f scrypted/compose.yml up -d
```

`pihole`, `mosquitto`, `dht22`, and `caddy` need the `homelab` network to exist first (they'll fail to start otherwise); `homebridge` and `scrypted` don't use it at all (see Homebridge networking above). Then complete the one-time setup steps above (wildcard DNS in Pi-hole, trusting Caddy's CA, the Homebridge setup wizard) before everything is reachable end to end.
