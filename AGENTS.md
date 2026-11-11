# AGENTS.md

Guidance for AI coding agents working in this repository.

## Commands

There is no test suite, linter, or CI in this repo. The available correctness checks are:

```bash
# Validate a compose file (interpolates env vars, checks YAML/schema — catches most mistakes)
docker compose -f <service>/compose.yml config

# Syntax-check the one piece of application code in the repo
python3 -m py_compile dht22/app/reader.py
```

Deploying/operating a service (each is an independent Compose project — see Architecture):

```bash
# one-time, before starting any service that joins the shared network
docker network create --ipv6 homelab

docker compose -f <service>/compose.yml up -d       # start (add --build for dht22, it's the only built image)
docker compose -f <service>/compose.yml logs -f      # logs
docker compose -f <service>/compose.yml down         # stop
```

## Architecture

### Multi-project Compose layout
Each service lives in its own top-level directory with its own `compose.yml` (not `docker-compose.yml`) — there is no root compose file. Every directory is a separate Compose project, deployed and managed independently. The direct consequence: each project gets its own default Docker network, so services cannot reach each other by container name unless explicitly wired together (see below).

### The `homelab` shared network
Cross-service traffic goes over an external Docker network named `homelab`, created once (`docker network create --ipv6 homelab`) and joined by declaring `networks: { homelab: { external: true } }` in a service's compose file. `pihole`, `mosquitto`, `dht22`, and `caddy` all join it so they can address each other by container name (e.g. Caddy → `pihole:80`, `dht22` → `mosquitto:1883`). `homebridge` is a deliberate exception — see below. The network is created with `--ipv6` so Docker forwards IPv6 DNS queries to Pi-hole via DNAT, keeping each client's real address; without it, every IPv6 client shows up in Pi-hole as the bridge gateway (`172.18.0.1`). Details in `README.md` under "Shared network".

### Data pipeline: DHT22 → MQTT → Homebridge → Apple Home
`dht22/app/reader.py` samples the sensor (wired to GPIO4 on a Raspberry Pi 5 — GPIO is exposed at `/dev/gpiochip4`, not `gpiochip0` as on older Pi models, because of the Pi 5's RP1 southbridge chip) every `SAMPLE_INTERVAL_SECONDS` (15s), and every `PUBLISH_INTERVAL_SECONDS` (120s) averages that window's sub-readings with a plain arithmetic mean — deliberately unweighted, since equal weighting minimizes variance for a slowly-changing signal like a basement cabinet, whereas recency-weighting would trade noise reduction for responsiveness that isn't needed here. It computes a dew point (Magnus-Tetens formula, Alduchov–Eskridge constants) from the averaged values and publishes three retained MQTT topics to `mosquitto`: `home/dht22/temperature`, `home/dht22/humidity`, `home/dht22/dew_point`. `homebridge` subscribes to these via the `homebridge-mqttthing` plugin (configured live through Homebridge's own UI — not checked into this repo) to expose them as HomeKit accessories.

`dht22/compose.yml` sets `BLINKA_FORCECHIP`/`BLINKA_FORCEBOARD` — without them, `import board` raises `NotImplementedError`. Docker masks `/sys/firmware` in unprivileged containers, and `/proc/device-tree` (what Blinka's autodetection reads to identify the Pi) is a symlink into it, so detection always fails in this container regardless of which Pi model it's actually on. There's no working bind-mount fix for this (confirmed broken upstream in Docker and Home Assistant's own issue trackers) short of `--privileged`, so the env vars bypass autodetection entirely instead. Forcing `RASPBERRY_PI_5` selects Blinka's Pi 5 pin backend. Since Blinka 10 that backend is built on the `gpiod` Python package (`requirements.txt`), which Blinka does not declare as a dependency — without it `import adafruit_dht` fails with `The platform library 'gpiod' was not found`. Older Blinka releases used `lgpio` here instead (Pi 5's RP1 chip isn't supported by the older `RPi.GPIO`); it is kept in `requirements.txt` because Blinka still uses it for PWM. The backend scans `/dev/gpiochip*` and picks the chip by its driver label (`pinctrl-rp1` on a Pi 5), because the number the kernel assigns RP1's GPIO driver is not stable across kernel versions (Raspberry Pi's own kernel has moved it between 0, 4, and other numbers across releases). A specific `devices:` passthrough for one chip number is fragile against this — `dht22/compose.yml` runs `privileged: true` instead, trading a broader-than-minimal container for not having to chase the chip number on every kernel update.

### Homebridge breaks every networking convention here, on purpose
HomeKit's mDNS/Bonjour advertisement and dynamic per-accessory TCP ports don't survive Docker's bridge-mode NAT, so `homebridge/compose.yml` uses `network_mode: host` — which Docker Compose treats as mutually exclusive with `networks:`, so it cannot join `homelab` like everything else. Consequences that follow from this, and matter if you touch networking here: Homebridge reaches Mosquitto via `mqtt://127.0.0.1:1883` (the host's loopback, since Mosquitto's port is already published to the host) instead of the container name `mosquitto`; and Caddy reaches Homebridge's web UI via `extra_hosts: ["host.docker.internal:host-gateway"]` → `host.docker.internal:8581` instead of the container name `homebridge`. Full reasoning (mDNS advertiser choice, avahi-daemon coexistence, router-level gotchas) is in `README.md` under "Homebridge networking."

### Scrypted: the second host-network exception
`scrypted/compose.yml` exposes the UniFi Protect doorbell to Apple Home (the camera used to go through a Homebridge plugin; Scrypted replaced it for lower stream and talk latency, since it prebuffers the stream and passes the camera's H.264 through without transcoding). It uses `network_mode: host` for the same HomeKit reasons as Homebridge, so the same consequences apply: it does not join `homelab`, Caddy reaches its UI via `host.docker.internal:10443` (Scrypted's own self-signed HTTPS, hence `tls_insecure_skip_verify` in the `scrypted.lan` block), and it shares the host's `avahi-daemon` through the same D-Bus/socket mounts. Plugins, the Protect login, and per-camera settings are configured in Scrypted's UI and live in `scrypted/data` — not checked in. The image is the `:lite` tag (no Python/object-detection stack; the UniFi Protect and HomeKit plugins don't need it). Upstream's compose file also ships Watchtower auto-updates and hardcoded public DNS servers; both are deliberately left out (nothing else here auto-updates, and the DNS override would bypass Pi-hole). Homebridge stays for the DHT22 accessories.

### Caddy: single ingress + custom local domain
`caddy` is the only service bound to host ports 80/443. It reverse-proxies friendly hostnames under `.lan` to each service's web UI (e.g. `pihole.lan → pihole:80`) — new sites are added as blocks in `caddy/Caddyfile`. `.local` is out because it's intercepted by mDNS/Bonjour resolvers on macOS, iOS, Linux, and Windows before a real DNS query would ever fire. `home.arpa` (the IETF-standardized domain for exactly this, RFC 8375) was tried first and rejected: Pi-hole FTL (v6.7) hardcodes RFC 8375 compliance for `home.arpa` specifically, always answering it locally and refusing to forward it — and the documented fix for this (`dns.domain.name`/`dns.domain.local`, FTL PR #2772) did not work even when confirmed correctly applied in FTL's live config. `.lan` avoids this entirely since it's Pi-hole's own **default** local domain (`dns.domain.name` defaults to `"lan"`, `dns.domain.local` defaults to `true`) — no hardcoded special-case behavior to fight. TLS is issued from Caddy's own internal CA (`local_certs` in the Caddyfile) rather than a public one — there's no way to get a publicly-trusted cert for a made-up local domain — so client devices need that CA trusted once (steps in `README.md`).

### Conventions for any new service
- Every service sets a `logging:` block capping container logs (`json-file` driver, `max-size: "10m"`, `max-file: "3"`, ~30MB/container) — Docker's default `json-file` driver has no size limit at all, and this is a Pi with an SD card, not a server with disks to spare.
- One flat `./data` directory per service for runtime state, bind-mounted directly (e.g. `./data:/etc/pihole`) — no nested subfolders. `data/` is blanket-gitignored. Static, version-controlled config lives outside `data/` instead (e.g. `mosquitto/config/mosquitto.conf`, `caddy/Caddyfile`).
- `.env` is used when a value is a secret (e.g. `pihole/.env` → `PIHOLE_PASSWORD`) or genuinely deployment-specific — would differ on another Pi/network (e.g. `PIHOLE_LAN_IP`) — interpolated via `${VAR}` either way; config that's identical regardless of deployment (`TZ`, ports, intervals) stays directly in `compose.yml` under `environment:`.
- `container_name` is always set explicitly to match the service name; `restart: unless-stopped` is always used.
