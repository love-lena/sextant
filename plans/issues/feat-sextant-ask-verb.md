---
title: Add `sextant ask <agent> "<text>"` — synchronous prompt+wait+print for daily-drive
status: open
priority: P3
created_at: 2026-05-25T14:53-07:00
labels: [feature, cli, ergonomics, assistant]
discovered_in: assistant-agent daily-drive setup
---

## Summary

Today, talking to an agent from the operator's terminal takes three commands:

```bash
sextant conversation <agent-uuid> > /tmp/log &
PID=$!
sextant agents prompt <agent-uuid> "..."
sleep 25
kill $PID
cat /tmp/log
```

The pieces exist (`prompt_agent` RPC + `agents.<uuid>.frames` subscribe), but glueing them takes ergonomic work the operator has to remember every time. For an assistant agent that's daily-driven, a synchronous one-shot is the natural shape.

## Proposed

```
sextant ask <agent> "<text>" [--timeout 60s] [--json]
```

Behavior:
1. Subscribe to `agents.<uuid>.frames` and `agents.<uuid>.lifecycle` *before* publishing the prompt (so the subscription is ready to capture the first frame).
2. Publish the prompt via `prompt_agent` RPC.
3. Stream frames + lifecycle envelopes inline as they arrive, same renderer as `sextant conversation`.
4. Exit cleanly on the next `lifecycle transition=turn_ended` (or `transition=ended` for a session-end), OR on `--timeout` expiry.
5. `--json` swaps to NDJSON output, same as `sextant tail --json` and `sextant conversation --json`.

`--timeout` defaults to 60s. Hard-cap turn duration so a stuck agent doesn't hang the terminal forever.

Name resolution: accept agent name OR UUID, like `sextant agents archive` already does (sister issue: [[bug-name-resolution-inconsistent-across-agents-verbs]]).

## Why P3 not P2

Existing two-pane workflow works. This is pure ergonomics. But it's the single biggest QOL improvement for an operator daily-driving an assistant — repetition adds up.

## Acceptance

`TestAskRendersAssistantReplyAndExits`:
1. Daemon up; spawn mock-driver agent that responds "ack" + emits `turn_ended` after one prompt.
2. `sextant ask <uuid> "smoke"`.
3. Assert process exits 0 within `--timeout`; stdout includes `[assistant] ack` and `[lifecycle] transition=turn_ended`.

`TestAskTimeoutExits`: agent never emits `turn_ended`; `sextant ask --timeout 2s ...` exits non-zero with a clear "timeout waiting for turn_ended" message within 2-3s.

## Related

- `cmd/sextant/conversation.go` — subscribe+render half of the implementation
- `cmd/sextant/agents.go::prompt` — publish half
- `[[bug-name-resolution-inconsistent-across-agents-verbs]]` — name resolution should be shared, not re-implemented per verb
- `[[bug-lifecycle-turn-ended-missing]]` (resolved at d4c45df) — turn_ended is now visible; this verb's exit condition depends on it
