---
title: client.toml NATS port hardcoded to 4222 — goes stale when sextantd auto-allocates a different port
status: fixed
priority: P2
created_at: 2026-05-25T14:53-07:00
labels: [bug, cli, config, client-libraries]
discovered_in: post-overnight daily-drive setup
fixed_in: ee663f52397dcb4b95ddef3d2c5e86986a25d5f2
---

## Summary

`sextant init` writes `~/.config/sextant/client.toml` with `[nats] url = "nats://127.0.0.1:4222"`. The matching sextantd default is `[nats] listen_port = 0` (auto-allocate). After any daemon start where 4222 is taken (or after a restart that picks a different free port), the client config points at the wrong port. `sextant doctor` works because it reads the live port from `runtime.json`; other CLI verbs that go through `pkg/client.Connect` hang on the connect timeout.

The actual live port is recorded in `~/.local/share/sextant/runtime.json::nats_addr`. The client just doesn't read it.

## Repro

1. Fresh `sextant init`; sextantd starts on some auto-allocated port (e.g. 53930).
2. `sextant doctor` — green.
3. `sextant agents list` — hangs ~30s, returns nothing (or exits 137 if killed by a process timeout).
4. `grep url ~/.config/sextant/client.toml` shows `nats://127.0.0.1:4222`.
5. `python3 -c "import json; print(json.load(open('/Users/lena/.local/share/sextant/runtime.json'))['nats_addr'])"` shows the real port.

Worked around twice during the overnight session by sed'ing the live port from runtime.json into client.toml. Documented as a known limitation in the overnight summary.

## Impact

- Operators have to manually patch client.toml after every daemon restart that picks a different port.
- This is the silent failure mode: command hangs, then returns nothing or exits, with no error message pointing at the cause.
- Hits every non-doctor verb (agents list, conversation, prompt, tail, audit, ...) since they all use the same `pkg/client.Connect`.

## Proposed fix (two viable shapes)

**Option A — sextantd rewrites client.toml on each startup.** The daemon already writes `runtime.json` on bind. Add a second write of `~/.config/sextant/client.toml` with the live `nats://127.0.0.1:<live_port>`. Preserves the operator-edited shape of the file (auth, timeouts) by overwriting only the `[nats] url` line. Touches operator config from sextantd — that's the same boundary that the existing `runtime.json` write crosses, so not a new norm.

**Option B (recommended) — client.Connect reads runtime.json first, falls back to client.toml.** The connection logic does: "if `runtime.json` exists and is newer than client.toml, use its `nats_addr`; else use client.toml's url." Daemon doesn't touch client.toml. The client uses the live address transparently. The original client.toml stays operator-curated for non-port fields (creds path, timeouts).

Lean **B**. Cleaner separation: sextantd owns runtime.json, operator owns client.toml. The client knows about both. This also keeps client.toml stable across daemon restarts (no surprise edits from the daemon).

## Acceptance

`TestClientConnectsToLiveNatsPortAcrossRestart`:
1. Start sextantd with listen_port=0; record allocated port P1.
2. `sextant agents list` — succeeds.
3. Stop sextantd, restart; daemon allocates port P2 != P1.
4. `sextant agents list` — succeeds without touching client.toml.

## Related

- `pkg/sextantd/config.go` — sextantd writes runtime.json on bind; this is where the daemon-side option would land
- `pkg/client/*` — client connection path; this is where the runtime.json-first read would land
- Mentioned in `plans/overnight-summary-2026-05-25.md` as a known gap
