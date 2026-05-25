---
title: claude_seed read-only bind-mount breaks SDK session persistence — multi-turn conversations fail
status: open
priority: P1
created_at: 2026-05-25T14:23-07:00
labels: [bug, template, sidecar, agent-memory, sessions]
discovered_in: first assistant-agent daily-drive setup
---

## Summary

`claude_seed` (shipped in `3c33d30`) bind-mounts the host's seed dir read-only at `/home/agent/.claude/`. This works for serving the seed contents (CLAUDE.md, custom commands, settings) to the agent — but it also blocks the Claude Agent SDK from writing its session journal (`~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`). The SDK silently doesn't persist the session, so when the next turn tries to resume via `SEXTANT_SESSION_ID`, it fails:

```
[error] message="No conversation found with session ID: 0f588a13-7ec2-4a8f-b52d-3e07df62e08a"
[lifecycle] transition=turn_ended reason="error"
```

The agent CAN still process the prompt (responds correctly) but the session_id persisted to NATS KV via CAS points at a session that doesn't exist on disk inside the container. Every "next turn" starts fresh.

## Repro

1. Write `~/.config/sextant/assistant-claude/CLAUDE.md` with any content.
2. Write a template `assistant.toml` with `claude_seed = "~/.config/sextant/assistant-claude"`.
3. `sextant templates reload`, `sextant agents spawn assistant --template assistant`.
4. Prompt the agent: `sextant agents prompt <uuid> "remember the number 7"` — turn 1 succeeds.
5. Prompt again: `sextant agents prompt <uuid> "what number did I tell you?"` — turn 2 fails with the "No conversation found" error.

Reproduced 2026-05-25 14:22 PT during assistant agent setup, immediately after `3c33d30` shipped.

## Impact

**Blocks the daily-drive assistant use case** — the literal motivating use case for `feat-template-claude-seeding`. Any agent that needs both pre-seeded memory AND multi-turn conversation hits this. Lead/dev agents that don't use `claude_seed` are unaffected.

## Root cause

`pkg/rpc/handlers/spawn.go::buildContainerSpec` (or wherever the seed mount is applied) declares the bind-mount RO. SDK writes to:

- `/home/agent/.claude/projects/<encoded-cwd>/<session-id>.jsonl` — per-session journal
- `/home/agent/.claude/.credentials.json` (if OAuth flow used) — would fail similarly
- `/home/agent/.claude/settings.local.json` — agent-side overrides
- `/home/agent/.claude/cache/` etc. — various SDK working state

All fail silently on a RO mount, leaving the agent's runtime state inconsistent with what NATS KV thinks it persisted.

## Proposed fix

**Option A (recommended) — copy-on-spawn**: introduce `claude_seed_mode` template field with values:

- `"readonly-bind"` (current behavior) — bind-mount RO. Suitable for agents that genuinely don't need to write to `~/.claude`, e.g. one-shot doc-reading agents. Documented as the explicit opt-in.
- `"copy-on-spawn"` (new default when `claude_seed` is set) — at spawn time, sextantd copies the host seed dir into a fresh per-incarnation named volume (or per-incarnation tmpfs), bind-mounts the writable copy at `/home/agent/.claude/`. The agent reads the seed contents AND can write its working state. Writes don't propagate back to the host source — they live in the named volume, which can persist across incarnations of the same agent if you key the volume name to `agent_uuid` (allowing session resume across restart).

**Option B — selective RO mount**: bind-mount specific sub-paths RO (`CLAUDE.md`, `commands/`, `agents/`) while leaving `projects/`, `cache/`, etc. writable in a per-agent volume. More complex; matches the "expose user memory, hide working state" intuition exactly but the path list has to track upstream Claude Code changes.

Lean: **Option A** (copy-on-spawn) — simpler, robust to upstream SDK layout changes, gives the assistant agent the daily-drive shape it actually wants (memory carries across daemon restarts via the per-agent named volume). The host source stays operator-curated and immutable from the agent's POV.

## Acceptance

`TestAssistantMultiTurnWithSeed`:
1. Seed dir at `/tmp/seed-test/` with `CLAUDE.md: "remember the number 42 if asked."`
2. Template with `claude_seed = "/tmp/seed-test"` and `claude_seed_mode = "copy-on-spawn"`.
3. Spawn assistant; prompt `"remember the number 7"` — wait for turn_ended.
4. Prompt `"what number did I tell you?"` — assert reply contains `7` (proves session continuity).
5. Kill agent; restart with `--preserve-session`; prompt `"and what number was that?"` — assert reply still contains `7` (proves the per-agent volume persists across incarnations).
6. `docker exec <container> ls /home/agent/.claude/projects/` — assert at least one `*.jsonl` file exists (proves SDK wrote its journal).

Plus `TestClaudeSeedReadonlyModeBlocksWrite` (regression for the original "RO bind" behavior remaining available when explicitly requested).

## Related

- `feat-template-claude-seeding.md` (resolved by `3c33d30`) — shipped the seed but didn't anticipate this gap
- `bug-restart-no-api-key-forwarding.md` + `bug-restart-preserve-session-noop.md` (both resolved overnight) — restart with `--preserve-session` works for agents without `claude_seed`; this bug specifically affects seeded agents
- Workaround for now: drop `claude_seed` from the assistant template; inline charter content in `initial_prompt`. Loses the multi-file memory shape but preserves multi-turn conversation. Re-enable seed once Option A ships.
