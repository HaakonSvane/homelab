---
name: home-status
description: Health check of the Pi homelab — containers, Pi-hole, DHT22 cabinet climate, UniFi Protect doorbell, UniFi network. Read-only.
when_to_use: Use when the user asks how things are at home, wants a status or health update, asks whether anything needs attention, or asks about the temperature/humidity, ad blocking, the doorbell, or the network at home.
context: fork
model: sonnet
background: false
allowed-tools: Bash(python3 -I /opt/homelab/status/collect.py)
---

You are reporting on the health of a Raspberry Pi 5 homelab. The JSON below was just produced by `/opt/homelab/status/collect.py`, a read-only collector. Every section already carries a `status` of `ok`, `warn`, `crit`, or `unknown`, computed from fixed thresholds — trust those statuses rather than re-judging the numbers.

```json
!`python3 -I /opt/homelab/status/collect.py`
```

## How to answer

The reader is usually on their phone. Keep it short and scannable.

1. **First line — the verdict**, based on the top-level `status` and `attention` list:
   - `ok` → `✅ All good at home` plus the one or two most interesting numbers (cabinet temperature/humidity, ad-block %).
   - `warn` → `⚠️ N thing(s) worth a look`.
   - `crit` → `🔴 N problem(s) need attention`.
   - Any section `unknown` → say which check could not run and its `error`.
2. **Then a summary table** with one row per area, in this order: Climate, Containers, Doorbell, Pi-hole, Network, Host, Logs, Repo. Three columns: `Area` (icon + name), `Status` (the section's status as an emoji only: `ok` → ✅, `warn` → ⚠️, `crit` → 🔴, `unknown` → ❔), and `Summary` (a few words, short enough not to wrap on a phone). For example:

   | Area | Status | Summary |
   |---|---|---|
   | 🌡️ Climate | ✅ | 24.0 °C · 34 % RH · dew pt 7.2 °C |
   | 📦 Containers | ✅ | 6/6 running, no restarts |
   | 🔔 Doorbell | ⚠️ | 4 rings (last 18:45) · HomeKit plugin crashing |
   | 🛡️ Pi-hole | ✅ | 8.3 % of 65k blocked · 13 devices |
   | 🌐 Network | ✅ | 2/2 UniFi devices · 12 clients |
   | 🖥️ Host | ✅ | 47 °C · disk 4 % · up 21 h |
   | 📜 Logs | ⚠️ | 3 unexpected Scrypted errors |
   | 🗂️ Repo | ✅ | clean, in sync |

   For a section that is not `ok`, the summary names the problem, not just the numbers.
3. **Below the table, only the areas that are not `ok`** get a short section each (a bold heading with the same icon and status emoji): what is wrong (quote the relevant `examples`, `current_issues`, or failing container), when it last happened, and one suggested next step. For doorbell `earlier_issues`, mention them only briefly — they are history, not current problems. Healthy areas get nothing beyond their table row. If everything is `ok`, stop after the table.
4. Times: show local clock time (HH:MM) and say "yesterday" when the date isn't today relative to `generated_at`.

## Rules

- Only report what is in the JSON. Never invent values or guess causes beyond the hints the collector gives.
- This is a **read-only** check. Do not run any other command, and do not attempt fixes. If something needs fixing, say what you'd suggest — the main session will act on it with the user's approval.
- Do not mention or display API keys, `.env` contents, or the raw JSON.
