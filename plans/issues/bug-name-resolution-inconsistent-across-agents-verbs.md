---
title: `sextant agents` verbs inconsistently accept names vs UUIDs
status: fixed
priority: P3
created_at: 2026-05-25T14:53-07:00
fixed_in: 4fb95d9583046315d1ddffdc492073a9930ace5b
labels: [bug, cli, agents-cli, ergonomics]
discovered_in: assistant-agent daily-drive setup
---

## Summary

The `sextant agents <verb>` family disagrees on whether `<agent>` arguments accept names. Today (post-wave-1-3):

| Verb | Accepts name? | Accepts UUID? |
|---|---|---|
| `agents archive` | ✓ | ✓ |
| `agents kill` | ? (need to verify, but `--all-dead` works) | ✓ |
| `agents restart` | ? | ✓ |
| `agents prompt` | ✗ (`invalid UUID length: 9`) | ✓ |
| `agents show` | ✗ | ✓ |
| `agents spawn` | n/a (creates a name) | n/a |

dev-6's archive verb (`5778027`) explicitly resolves names via list_agents. Other verbs predate that work and don't.

Verified during the daily-drive assistant setup: `sextant agents prompt assistant "..."` errors with `sextant: agent_uuid: invalid UUID length: 9`. Same shape as `sextant agents show smoke-1` earlier in the session.

## Impact

- Operators have to remember UUIDs or shell-script the name→UUID resolution.
- Inconsistent UX: `archive` works with names; `prompt`, the most-used verb in daily drive, doesn't.
- Documentation (cli/commands.md) implies all verbs accept `<agent>` uniformly — no warning that some require UUIDs.

The assistant agent example: I gave the user a `alias ask='sextant agents prompt $ASSISTANT_UUID'` workaround in the daily-drive instructions. That'd be unnecessary if `prompt` resolved names.

## Proposed fix

Extract the resolution logic from `cmd/sextant/agents.go::archive` (where dev-6 implemented it) into a shared helper:

```go
// resolveAgentRef looks up an agent by UUID or name. UUID-shaped input is
// returned as-is; otherwise calls list_agents and matches by name among
// non-archived entries (matching dev-6's archive semantics).
func resolveAgentRef(ctx context.Context, cli *client.Client, ref string) (uuid.UUID, error)
```

Wire into every `agents <verb>` that takes an `<agent>` argument: kill, restart, prompt, show. Each verb's existing UUID-parse fast-path stays (no regression on UUID input).

Document in `specs/cli/commands.md` that all `<agent>` arguments accept name OR UUID.

## Acceptance

`TestAgentsPromptByName`:
1. Spawn `agent-foo`; capture UUID.
2. `sextant agents prompt agent-foo "smoke"` succeeds (publishes the prompt envelope on the inbox by UUID).
3. Assert the prompt landed on `agents.<UUID>.inbox` (not just that the CLI exited 0).

Plus analogous tests for show, kill, restart.

## Related

- `cmd/sextant/agents.go` — archive resolves names; other verbs don't
- `5778027 feat(archive)` — added name resolution as part of the archive verb, didn't share it
- `[[feat-sextant-ask-verb]]` — the new `ask` verb should accept names from day one (the shared helper makes that free)
