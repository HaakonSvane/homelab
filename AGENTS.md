# AGENTS.md

Guidance for AI coding agents working in this repository.

## Commands

There is no test suite, linter, or CI in this repo. The available correctness checks are:

```bash
# Validate a compose file (interpolates env vars, checks YAML/schema — catches most mistakes)
docker compose -f <service>/compose.yml config

# Syntax-check the application code in the repo
python3 -m py_compile dht22/app/reader.py status/collect.py

# Finance app (Go): no toolchain on the Pi, dev.sh runs go in a golang container
cd finance && ./dev.sh tool templ generate && ./dev.sh vet ./... && ./dev.sh test ./...

# Run the read-only home health collector (prints JSON; used by the home-status skill)
python3 -I status/collect.py
```

Deploying/operating a service (each is an independent Compose project — see Architecture):

```bash
# one-time, before starting any service that joins the shared network
docker network create --ipv6 homelab

docker compose -f <service>/compose.yml up -d       # start (add --build for dht22 and finance, the built images)
docker compose -f <service>/compose.yml logs -f      # logs
docker compose -f <service>/compose.yml down         # stop
```

## Architecture

### Multi-project Compose layout
Each service lives in its own top-level directory with its own `compose.yml` (not `docker-compose.yml`) — there is no root compose file. Every directory is a separate Compose project, deployed and managed independently. The direct consequence: each project gets its own default Docker network, so services cannot reach each other by container name unless explicitly wired together (see below).

### The `homelab` shared network
Cross-service traffic goes over an external Docker network named `homelab`, created once (`docker network create --ipv6 homelab`) and joined by declaring `networks: { homelab: { external: true } }` in a service's compose file. `pihole`, `mosquitto`, `dht22`, `caddy` and `finance` all join it so they can address each other by container name (e.g. Caddy → `pihole:80`, `dht22` → `mosquitto:1883`, Caddy → `finance:8080`). `homebridge` is a deliberate exception — see below. The network is created with `--ipv6` so Docker forwards IPv6 DNS queries to Pi-hole via DNAT, keeping each client's real address; without it, every IPv6 client shows up in Pi-hole as the bridge gateway (`172.18.0.1`). Details in `README.md` under "Shared network".

### Data pipeline: DHT22 → MQTT → Homebridge → Apple Home
`dht22/app/reader.py` samples the sensor (wired to GPIO4 on a Raspberry Pi 5 — GPIO is exposed at `/dev/gpiochip4`, not `gpiochip0` as on older Pi models, because of the Pi 5's RP1 southbridge chip) every `SAMPLE_INTERVAL_SECONDS` (15s), and every `PUBLISH_INTERVAL_SECONDS` (120s) averages that window's sub-readings with a plain arithmetic mean — deliberately unweighted, since equal weighting minimizes variance for a slowly-changing signal like a basement cabinet, whereas recency-weighting would trade noise reduction for responsiveness that isn't needed here. It computes a dew point (Magnus-Tetens formula, Alduchov–Eskridge constants) from the averaged values and publishes three retained MQTT topics to `mosquitto`: `home/dht22/temperature`, `home/dht22/humidity`, `home/dht22/dew_point`. `homebridge` subscribes to these via the `homebridge-mqttthing` plugin (configured live through Homebridge's own UI — not checked into this repo) to expose them as HomeKit accessories.

`dht22/compose.yml` sets `BLINKA_FORCECHIP`/`BLINKA_FORCEBOARD` — without them, `import board` raises `NotImplementedError`. Docker masks `/sys/firmware` in unprivileged containers, and `/proc/device-tree` (what Blinka's autodetection reads to identify the Pi) is a symlink into it, so detection always fails in this container regardless of which Pi model it's actually on. There's no working bind-mount fix for this (confirmed broken upstream in Docker and Home Assistant's own issue trackers) short of `--privileged`, so the env vars bypass autodetection entirely instead. Forcing `RASPBERRY_PI_5` selects Blinka's Pi 5 pin backend. Since Blinka 10 that backend is built on the `gpiod` Python package (`requirements.txt`), which Blinka does not declare as a dependency — without it `import adafruit_dht` fails with `The platform library 'gpiod' was not found`. Older Blinka releases used `lgpio` here instead (Pi 5's RP1 chip isn't supported by the older `RPi.GPIO`); it is kept in `requirements.txt` because Blinka still uses it for PWM. The backend scans `/dev/gpiochip*` and picks the chip by its driver label (`pinctrl-rp1` on a Pi 5), because the number the kernel assigns RP1's GPIO driver is not stable across kernel versions (Raspberry Pi's own kernel has moved it between 0, 4, and other numbers across releases). A specific `devices:` passthrough for one chip number is fragile against this — `dht22/compose.yml` runs `privileged: true` instead, trading a broader-than-minimal container for not having to chase the chip number on every kernel update.

### Homebridge breaks every networking convention here, on purpose
HomeKit's mDNS/Bonjour advertisement and dynamic per-accessory TCP ports don't survive Docker's bridge-mode NAT, so `homebridge/compose.yml` uses `network_mode: host` — which Docker Compose treats as mutually exclusive with `networks:`, so it cannot join `homelab` like everything else. Consequences that follow from this, and matter if you touch networking here: Homebridge reaches Mosquitto via `mqtt://127.0.0.1:1883` (the host's loopback, since Mosquitto's port is already published to the host) instead of the container name `mosquitto`; and Caddy reaches Homebridge's web UI via `extra_hosts: ["host.docker.internal:host-gateway"]` → `host.docker.internal:8581` instead of the container name `homebridge`. Full reasoning (mDNS advertiser choice, avahi-daemon coexistence, router-level gotchas) is in `README.md` under "Homebridge networking."

### Scrypted: the second host-network exception
`scrypted/compose.yml` exposes the UniFi Protect doorbell to Apple Home (the camera used to go through a Homebridge plugin; Scrypted replaced it for lower stream and talk latency, since it prebuffers the stream and passes the camera's H.264 through without transcoding). It uses `network_mode: host` for the same HomeKit reasons as Homebridge, so the same consequences apply: it does not join `homelab`, Caddy reaches its UI via `host.docker.internal:10443` (Scrypted's own self-signed HTTPS, hence `tls_insecure_skip_verify` in the `scrypted.home.arpa` block), and it shares the host's `avahi-daemon` through the same D-Bus/socket mounts. Plugins, the Protect login, and per-camera settings are configured in Scrypted's UI and live in `scrypted/data` — not checked in. The image is the `:lite` tag (no Python/object-detection stack; the UniFi Protect and HomeKit plugins don't need it). Upstream's compose file also ships Watchtower auto-updates and hardcoded public DNS servers; both are deliberately left out (nothing else here auto-updates, and the DNS override would bypass Pi-hole). Homebridge stays for the DHT22 accessories.

### Finance app (`finance/`)
A Go web app at `https://finans.home.arpa` (Caddy → `finance:8080`) for the household's shared expense account. Read the README's "Finance (felleskonto)" section first. Things that are easy to get wrong:
- **Naming:** code, identifiers, directory, container and commits are English (`finance`). Everything a user sees is Norwegian bokmål: the host name `finans`, page text, errors, and URL paths (`/utgifter`, `/inntekt`, `/innstillinger`, `/logg-inn`, `/registrer`).
- **Money and maths:** amounts are `money.Ore` (int64 øre); splits are basis points (5000 = 50 %). There are no floats anywhere.
  - All settlement maths lives in `internal/ledger` as pure functions. It sums exactly in 1/120000 øre and rounds half-even only once per person, assigning leftover øre so that the transfers always sum to the felleskonto total. Change it only with tests.
  - Effective dating also lives in the ledger: the version for month M is the one with the greatest `valid_from <= M`, and `end_month` is inclusive. The store only loads and saves the history.
- **History:** never rewrite it. An edit saves a new version from a chosen month; saving for a month that already has a version replaces only that one. Schema changes are new numbered files in `internal/store/migrations/`; never edit a deployed one.
- **Auth:** passkeys only (`go-webauthn`), and the relying party ID is `finans.home.arpa`. Changing `FINANCE_ORIGIN` breaks every passkey. Users and enrollment links come from the CLI, never the web UI.
- **Front end:**
  - Templates are templ: edit `.templ` files, run `./dev.sh tool templ generate`, and commit the generated `*_templ.go`.
  - The CSP allows no inline scripts or styles, and the only custom JS is `web/static/passkey.js`.
  - Interactivity is htmx plus the `<dialog popover>` bottom sheet, with CSS `:has()` showing and hiding fields; plain form POST-redirect-GET must keep working.
  - Icons are Phosphor Light path data in `internal/web/components/icons.go` (MIT licence alongside).
  - `go:embed` serves `web/static/` with content-hashed URLs.
- **Time-based features:** `internal/budget` (pure) handles pay dates with Norwegian bank holidays, pay periods, spending pace and purchase advice. `ledger.BufferNeed` and `ledger.Upcoming` drive the overview's outlook sections. The overview calculators (`POST /kan-jeg-kjope`, `POST /felleskonto/saldo`) render the page with the answer instead of redirecting, and never store what was typed.
- **Container:** distroless `nonroot` (uid 65532), so `finance/data` must be owned by 65532. Nightly backups go to `finance/data/backups`, and `status/collect.py` checks their age.

### Caddy: single ingress + custom local domain
`caddy` is the only service bound to host ports 80/443. It reverse-proxies friendly hostnames under `home.arpa` to each service's web UI (e.g. `pihole.home.arpa → pihole:80`) — new sites are added as blocks in `caddy/Caddyfile`. `.local` is out because it's intercepted by mDNS/Bonjour resolvers on macOS, iOS, Linux, and Windows before a real DNS query would ever fire. `home.arpa` is the IETF-standardized domain for exactly this (RFC 8375), and `.arpa` is a real TLD, so browsers treat a bare `pihole.home.arpa` as an address (a made-up TLD like `.lan` gets sent to a search engine). The wildcard is a plain `address=/home.arpa/${PIHOLE_LAN_IP}` in `pihole/compose.yml`; `dns.domain.name`/`dns.domain.local` stay at their defaults. History: this failed to resolve on Pi-hole FTL v6.7, so the repo used `.lan` from August to November 2026; it works on FTL v6.8+. FTL's webserver trusts a single Host (`webserver.domain`, set to `pihole.home.arpa`), so serving Pi-hole under any additional name needs `header_up Host pihole.home.arpa` in its Caddy block. TLS is issued from Caddy's own internal CA (`local_certs` in the Caddyfile) rather than a public one — there's no way to get a publicly-trusted cert for a made-up local domain — so client devices need that CA trusted once (steps in `README.md`).

### Conventions for any new service
- Every service sets a `logging:` block capping container logs (`json-file` driver, `max-size: "10m"`, `max-file: "3"`, ~30MB/container) — Docker's default `json-file` driver has no size limit at all, and this is a Pi with an SD card, not a server with disks to spare.
- One flat `./data` directory per service for runtime state, bind-mounted directly (e.g. `./data:/etc/pihole`) — no nested subfolders. `data/` is blanket-gitignored. Static, version-controlled config lives outside `data/` instead (e.g. `mosquitto/config/mosquitto.conf`, `caddy/Caddyfile`).
- `.env` is used when a value is a secret (e.g. `pihole/.env` → `PIHOLE_PASSWORD`) or genuinely deployment-specific — would differ on another Pi/network (e.g. `PIHOLE_LAN_IP`) — interpolated via `${VAR}` either way; config that's identical regardless of deployment (`TZ`, ports, intervals) stays directly in `compose.yml` under `environment:`.
- `container_name` is always set explicitly to match the service name; `restart: unless-stopped` is always used.

### Home status check
`status/collect.py` (stdlib only, read-only) gathers host, container, log, Pi-hole, DHT22, doorbell, UniFi, finance-backup and repo health into one JSON report, with every threshold and status decided in code. The project skill `.claude/skills/home-status` (forked, `model: sonnet`) only turns that JSON into a summary. When adding a service, add it to `EXPECTED_CONTAINERS`. When a new harmless log line shows up, add a commented pattern to `BENIGN_LOG_PATTERNS` instead of loosening `ERROR_RE`. UniFi credentials live in `status/.env` (a read-only API key; see `README.md` "Home status & remote access").
