"""Read-only health snapshot of the homelab, printed as one JSON document.

Used by the `home-status` Claude Code skill (.claude/skills/home-status/SKILL.md),
which turns this output into a short summary. Every check is deterministic and
thresholded here, so the model only summarizes and never has to judge raw numbers.

Each section is {"status": "ok"|"warn"|"crit"|"unknown", ...facts}. A section that
fails to collect becomes "unknown" with an "error" field instead of aborting the run.
Nothing here writes anywhere: docker inspect/logs/exec of read-only queries, sqlite in
-readonly mode, a retained-MQTT read, and GET requests to the UniFi API.
"""

import json
import os
import re
import shutil
import ssl
import subprocess
import time
import urllib.request
from datetime import datetime, timezone

REPO = "/opt/homelab"
ENV_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env")

EXPECTED_CONTAINERS = ["pihole", "mosquitto", "dht22", "caddy", "homebridge", "scrypted"]
LOG_WINDOW = "24h"

DISK_WARN_PCT, DISK_CRIT_PCT = 80, 90
CPU_TEMP_WARN_C, CPU_TEMP_CRIT_C = 70, 80
GRAVITY_MAX_AGE_DAYS = 8
DHT_STALE_SECONDS = 10 * 60  # reader publishes every 120s
HUMIDITY_WARN_PCT = 65       # sustained RH above this risks mould in the cabinet

# FTL query status codes that mean "blocked" (gravity, regex, denylist, CNAME, special domains, ...).
PIHOLE_BLOCKED_STATUSES = "(1,4,5,6,7,8,9,10,11,15,16,18)"

ERROR_RE = re.compile(r"warn|error|fail|exception|denied|refused|timed? ?out|unable|cannot|could not|fatal|crash", re.I)
# Continuation lines of a multi-line error (JS/Python stack frames, object dumps): the
# error itself is already counted on its first line.
CONTINUATION_RE = re.compile(r"^(\s*at |\s*File \"|\s*[{}\[\]],?\s*$|\s+\w+: |Traceback)")

# Doorbell regressions only count as current if seen this recently; older ones are
# reported as history (e.g. issues already fixed earlier in the 24h window).
REGRESSION_WINDOW_SECONDS = 6 * 3600

# Log lines that match ERROR_RE but are known to be harmless (triaged 2026-11-11).
BENIGN_LOG_PATTERNS = {
    "dht22": [
        r"Read attempt \d+/\d+ failed",        # DHT22 checksum/timing retries, ~1% of reads, always recovered
    ],
    "caddy": [
        r"HTTP/[23] skipped because it requires TLS",  # :80 listener exists only to redirect
        r"certutil",                            # Caddy can't install its CA into the container's NSS store
        r"installing root certificate",
    ],
    "homebridge": [
        r"unable to determine which interface",  # side effect of network_mode: host
    ],
    "pihole": [
        r"/root/\.git",                         # version check expects a git checkout the image lacks
    ],
    "scrypted": [
        r"Unable to find sync frame",           # snapshot fallback; keyframe every 4s
        r"streaming session killed",            # normal end of every HomeKit live view
        r"This stream may fail",                # paired with "codec must be h264", tracked in doorbell section
        r"Mixin (error|device creation completed with error)",  # emitted on every HomeKit plugin reload
        r"RpcPeer has been killed",
        r"scheduled plugin restart cancelled",
        r"Transcoding Debug Mode",
        # Tracked (with recency) in the doorbell section instead of double-reported here.
        r"bind EINVAL",
        r"unexpectedly exited",
        # Side effects of a plugin restart/reload: every plugin's RPC pipe closes at once.
        r"@scrypted/homekit error Error: (disconnect|close)",
        r"Error: write EPIPE",
        r"exited null SIGTERM",
        r"^RPCResultError: connection closed\.$",
        # Logged when a phone closes the live view without a clean stop.
        r"HomeKit Streaming RTCP timed out",
        # Advisory version of the H.265 warning; the doorbell section tracks the real one.
        r"Video codec is not h264",
        # Protect's RTSP stream dropping and reconnecting by itself.
        r"stream ended: readLength done",
        r"rebroadcast error Error: (rtsp read loop exited|rebroadcast disabled)",
        r"attempting to change invalid idr interval",
    ],
    "mosquitto": [],
}

STATUS_RANK = {"ok": 0, "unknown": 1, "warn": 2, "crit": 3}


def worst(*statuses):
    return max(statuses, key=lambda s: STATUS_RANK[s]) if statuses else "ok"


def run(cmd, timeout=20):
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    return result.returncode, result.stdout, result.stderr


def docker_logs(name, since=LOG_WINDOW):
    # Containers write to both streams; merge them like `docker logs 2>&1`.
    _, out, err = run(["docker", "logs", "-t", "--since", since, name], timeout=30)
    return (out + err).splitlines()


def parse_docker_ts(line):
    """Return (epoch_seconds, rest) for a `docker logs -t` line, or (None, line)."""
    stamp, _, rest = line.partition(" ")
    try:
        dt = datetime.strptime(stamp[:19], "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc)
        return dt.timestamp(), rest
    except ValueError:
        return None, line


def iso(epoch):
    return datetime.fromtimestamp(epoch).astimezone().isoformat(timespec="seconds") if epoch else None


def read_env():
    env = {}
    try:
        with open(ENV_FILE) as fh:
            for line in fh:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, _, value = line.partition("=")
                    env[key.strip()] = value.strip().strip("'\"")
    except FileNotFoundError:
        pass
    return env


def sqlite(db, query):
    code, out, err = run(["docker", "exec", "pihole", "pihole-FTL", "sqlite3", "-readonly", db, query])
    if code != 0:
        raise RuntimeError(err.strip() or f"sqlite exit {code}")
    return [row.split("|") for row in out.strip().splitlines()]


# --- sections -------------------------------------------------------------------------


def host():
    with open("/proc/uptime") as fh:
        uptime_s = float(fh.read().split()[0])
    load1, load5, load15 = os.getloadavg()
    meminfo = {}
    with open("/proc/meminfo") as fh:
        for line in fh:
            key, value = line.split(":")
            meminfo[key] = int(value.split()[0])
    mem_used_pct = round(100 * (1 - meminfo["MemAvailable"] / meminfo["MemTotal"]), 1)
    disk = shutil.disk_usage("/")
    disk_pct = round(100 * disk.used / disk.total, 1)
    with open("/sys/class/thermal/thermal_zone0/temp") as fh:
        cpu_temp = round(int(fh.read()) / 1000, 1)
    throttled = None
    if shutil.which("vcgencmd"):
        _, out, _ = run(["vcgencmd", "get_throttled"])
        throttled = out.strip().partition("=")[2] or None

    status = "ok"
    if disk_pct >= DISK_CRIT_PCT or cpu_temp >= CPU_TEMP_CRIT_C:
        status = "crit"
    elif disk_pct >= DISK_WARN_PCT or cpu_temp >= CPU_TEMP_WARN_C or throttled not in (None, "0x0"):
        status = "warn"
    return {
        "status": status,
        "uptime_hours": round(uptime_s / 3600, 1),
        "load": [round(load1, 2), round(load5, 2), round(load15, 2)],
        "memory_used_pct": mem_used_pct,
        "disk_root_used_pct": disk_pct,
        "disk_root_free_gb": round(disk.free / 1e9, 1),
        "cpu_temp_c": cpu_temp,
        "throttled": throttled,
    }


def containers():
    code, out, _ = run(["docker", "inspect", *EXPECTED_CONTAINERS])
    found = {c["Name"].lstrip("/"): c for c in json.loads(out or "[]")}
    result, statuses = {}, []
    for name in EXPECTED_CONTAINERS:
        c = found.get(name)
        if c is None:
            result[name] = {"status": "crit", "state": "missing"}
            statuses.append("crit")
            continue
        state = c["State"]
        health = (state.get("Health") or {}).get("Status")
        restarts = c.get("RestartCount", 0)
        status = "ok"
        if state["Status"] != "running" or health == "unhealthy":
            status = "crit"
        elif restarts > 0:
            status = "warn"
        result[name] = {
            "status": status,
            "state": state["Status"],
            "health": health,
            "restarts": restarts,
            "started_at": state.get("StartedAt", "")[:19] + "Z",
        }
        statuses.append(status)
    return {"status": worst(*statuses), "containers": result}


def logs():
    per_container, statuses = {}, []
    for name in EXPECTED_CONTAINERS:
        benign = [re.compile(p, re.I) for p in BENIGN_LOG_PATTERNS.get(name, [])]
        unknown = []
        for line in docker_logs(name):
            _, text = parse_docker_ts(line)
            if CONTINUATION_RE.match(text):
                continue
            if ERROR_RE.search(text) and not any(p.search(text) for p in benign):
                unknown.append(text.strip())
        status = "warn" if unknown else "ok"
        entry = {"status": status, "unexpected_error_lines": len(unknown)}
        if unknown:
            # Distinct examples, most recent first.
            entry["examples"] = list(dict.fromkeys(reversed(unknown)))[:3]
        per_container[name] = entry
        statuses.append(status)
    return {"status": worst(*statuses), "window": LOG_WINDOW, "containers": per_container}


def pihole():
    ftl = "/etc/pihole/pihole-FTL.db"
    day = "strftime('%s','now','-24 hours')"
    total, blocked, clients = sqlite(
        ftl,
        f"select count(*), sum(status in {PIHOLE_BLOCKED_STATUSES}), count(distinct client) "
        f"from queries where timestamp > {day}",
    )[0]
    last_hour = int(sqlite(ftl, "select count(*) from queries where timestamp > strftime('%s','now','-1 hour')")[0][0])
    info = dict(sqlite("/etc/pihole/gravity.db", "select property, value from info"))
    gravity_updated = int(info.get("updated", 0))
    gravity_age_days = round((time.time() - gravity_updated) / 86400, 1) if gravity_updated else None
    lists = [row[0] for row in sqlite("/etc/pihole/gravity.db", "select address from adlist where enabled=1")]

    total, blocked = int(total), int(blocked or 0)
    status = "ok"
    if last_hour == 0:
        status = "crit"
    elif gravity_age_days is None or gravity_age_days > GRAVITY_MAX_AGE_DAYS:
        status = "warn"
    return {
        "status": status,
        "queries_24h": total,
        "blocked_24h": blocked,
        "blocked_pct_24h": round(100 * blocked / total, 1) if total else 0.0,
        "clients_24h": int(clients),
        "queries_last_hour": last_hour,
        "gravity_domains": int(info.get("gravity_count", 0)),
        "gravity_updated": iso(gravity_updated),
        "gravity_age_days": gravity_age_days,
        "adlists": lists,
    }


PUBLISHED_RE = re.compile(r"Published temperature=([-\d.]+)C humidity=([\d.]+)% dew_point=([-\d.]+)C")


def climate():
    readings = []
    for line in docker_logs("dht22"):
        ts, text = parse_docker_ts(line)
        match = PUBLISHED_RE.search(text)
        if ts and match:
            readings.append((ts, *map(float, match.groups())))

    retained = {}
    code, out, _ = run(
        ["docker", "exec", "mosquitto", "mosquitto_sub", "-t", "home/dht22/#", "-v", "-C", "3", "-W", "3"],
    )
    for line in out.splitlines():
        topic, _, value = line.partition(" ")
        retained[topic.rsplit("/", 1)[-1]] = float(value)

    if not readings:
        return {"status": "crit", "error": "no dht22 readings published in the last " + LOG_WINDOW, "mqtt_retained": retained}

    last_ts, temp, hum, dew = readings[-1]
    age = time.time() - last_ts
    temps, hums = [r[1] for r in readings], [r[2] for r in readings]
    status = "ok"
    if age > DHT_STALE_SECONDS or len(retained) < 3:
        status = "crit"
    elif hum >= HUMIDITY_WARN_PCT:
        status = "warn"
    return {
        "status": status,
        "temperature_c": temp,
        "humidity_pct": hum,
        "dew_point_c": dew,
        "last_published": iso(last_ts),
        "minutes_since_publish": round(age / 60, 1),
        "temperature_24h_min_max": [min(temps), max(temps)],
        "humidity_24h_min_max": [min(hums), max(hums)],
        "mqtt_retained": retained,
    }


DOORBELL_REGRESSIONS = {
    # key: (log line prefix, human description with likely cause). Prefixes pick the one
    # line Scrypted logs per occurrence (it echoes several of these with an "e HomeKit" copy).
    "homekit_plugin_crash": ("e HomeKit plugin @scrypted/homekit unexpectedly exited", "HomeKit plugin crashed"),
    "ipv6_bind_error": ("unhandledRejection Error: bind EINVAL", "IPv6 stream bind error (Scrypted server address unset?)"),
    "h265_stream": ("[Doorbell Lite] Doorbell Lite video codec must be h264", "H.265 stream (Protect Enhanced Encoding re-enabled?)"),
}


def doorbell():
    rings, motions = [], 0
    seen = {key: [] for key in DOORBELL_REGRESSIONS}
    for line in docker_logs("scrypted"):
        ts, text = parse_docker_ts(line)
        if "Doorbell Lite binaryState: true" in text:
            rings.append(ts)
        elif "Doorbell Lite motionDetected: true" in text:
            motions += 1
        else:
            for key, (needle, _) in DOORBELL_REGRESSIONS.items():
                if text.startswith(needle):
                    seen[key].append(ts)

    now = time.time()
    current, history = [], []
    for key, stamps in seen.items():
        if not stamps:
            continue
        entry = {"issue": DOORBELL_REGRESSIONS[key][1], "count": len(stamps), "last_seen": iso(stamps[-1])}
        (current if now - stamps[-1] <= REGRESSION_WINDOW_SECONDS else history).append(entry)
    return {
        "status": "warn" if current else "ok",
        "window": LOG_WINDOW,
        "rings": len(rings),
        "last_ring": iso(rings[-1]) if rings else None,
        "motion_events": motions,
        "current_issues": current,
        "earlier_issues": history,
    }


def network():
    env = read_env()
    host_, key = env.get("UNIFI_HOST"), env.get("UNIFI_API_KEY")
    if not host_ or not key:
        return {"status": "unknown", "error": "UNIFI_HOST/UNIFI_API_KEY not set in status/.env"}
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE  # UniFi console uses a self-signed certificate on the LAN

    def get(path):
        req = urllib.request.Request(
            f"https://{host_}/proxy/network/integration/v1{path}",
            headers={"X-API-KEY": key, "Accept": "application/json"},
        )
        with urllib.request.urlopen(req, context=ctx, timeout=10) as resp:
            return json.load(resp)

    site = get("/sites")["data"][0]["id"]
    devices = get(f"/sites/{site}/devices?limit=200")["data"]
    clients = get(f"/sites/{site}/clients?limit=1")
    offline = [d.get("name") for d in devices if d.get("state") != "ONLINE"]
    return {
        "status": "crit" if offline else "ok",
        "devices": {d.get("name"): d.get("state") for d in devices},
        "offline_devices": offline,
        "clients_online": clients.get("totalCount"),
    }


def repo():
    _, porcelain, _ = run(["git", "-C", REPO, "status", "--porcelain"])
    _, counts, _ = run(["git", "-C", REPO, "rev-list", "--left-right", "--count", "origin/main...HEAD"])
    behind, ahead = (int(x) for x in counts.split()) if counts.strip() else (None, None)
    dirty = [line[3:] for line in porcelain.splitlines()]
    return {
        "status": "warn" if dirty or ahead else "ok",
        "uncommitted_files": dirty,
        "unpushed_commits": ahead,
        "behind_origin": behind,  # as of the last fetch; this check never fetches
    }


SECTIONS = {
    "host": host,
    "containers": containers,
    "logs": logs,
    "pihole": pihole,
    "climate": climate,
    "doorbell": doorbell,
    "network": network,
    "repo": repo,
}


def main():
    report = {"generated_at": datetime.now().astimezone().isoformat(timespec="seconds")}
    for name, collect in SECTIONS.items():
        try:
            report[name] = collect()
        except Exception as error:  # one broken source must not hide the others
            report[name] = {"status": "unknown", "error": f"{type(error).__name__}: {error}"}
    report["status"] = worst(*(report[name]["status"] for name in SECTIONS))
    report["attention"] = [name for name in SECTIONS if report[name]["status"] != "ok"]
    print(json.dumps(report, indent=1, ensure_ascii=False))


if __name__ == "__main__":
    main()
