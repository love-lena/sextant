---
id: TASK-20
title: >-
  Template `initial_prompt` is persisted to KV but never reaches the SDK —
  sidecar ignores it
status: Done
assignee: []
created_date: '2026-05-25 14:45'
labels:
  - bug
  - sidecar
  - sdk-wireup
  - template
  - 'slug:bug-initial-prompt-not-forwarded-to-sdk'
  - P2
  - 'closed:fixed'
dependencies: []
priority: medium
ordinal: 20000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

The `Template.InitialPrompt` field is defined, parsed from TOML (`pkg/templates/template.go:31`), read by the spawn handler (`pkg/rpc/handlers/spawn.go:198`), and persisted into the `AgentDefinition` in NATS KV (`pkg/sextantproto/agent.go:34` + the matching JSON schema). The trail then **dies in transit**: the sidecar entrypoint (`images/sidecar/entrypoint/src/index.ts`) has zero references to `InitialPrompt`, `initial_prompt`, or any related env var. The SDK driver receives the inbox prompt directly with no system-prompt prefix from the template.

Effect: the operator's curated charter / context-setting text never reaches the agent's first turn. The template field is decorative as far as agent behavior goes — it sits in KV unused.

## Repro

1. Write a template with a distinctive `initial_prompt` that the agent would only know from that field, e.g.:
   ```toml
   name = "smoke"
   description = "..."
   image = "sextant-sidecar:latest"
   permissions = ["read.agents", "control.prompt"]
   model = "claude-opus-4-7[1m]"
   permission_ceiling = "auto"
   initial_prompt = "Your secret codename is fnord-seven."
   ```
2. `sextant templates reload`, `sextant agents spawn smoke --template smoke`.
3. `sextant agents prompt <uuid> "what is your secret codename?"`
4. Agent reports it doesn't know — the `initial_prompt` text never reached the SDK.

Reproduced 2026-05-25 14:25 PT during the first daily-drive assistant setup: charter said "operator is Lena Hickson"; assistant replied "you're the operator" without the name.

## Impact

- Charter behavior set in `initial_prompt` is silently dropped — operators can't reliably establish role/context/preferences via the template.
- Workaround today: inline charter content into `CLAUDE.md` via `claude_seed`, OR send it as the literal first prompt before any "real" prompt. Both have ergonomic costs (claude_seed has its own RO bug — see `[[bug-claude-seed-readonly-breaks-session-persistence]]`; first-prompt-as-charter wastes a turn).
- The downstream impact is small for dev/lead agents (where charter is light) and load-bearing for assistant-style agents (where charter IS the identity).

## Proposed fix

Wire `initial_prompt` through to the SDK as a system prompt:

1. `pkg/rpc/handlers/container_env.go::buildContainerEnv` — read `def.InitialPrompt` and inject as `SEXTANT_INITIAL_PROMPT` env var when non-empty. Use base64 encoding if multi-line is too painful for the env-var path; otherwise the existing shell-quote handling should suffice for typical content.
2. `images/sidecar/entrypoint/src/index.ts::readEnv` — pick up `SEXTANT_INITIAL_PROMPT` (decode if base64-encoded).
3. `images/sidecar/entrypoint/src/index.ts::newSDKDriver` — pass to the SDK as `systemPrompt` (or whatever the current Claude Agent SDK option is named; verify against the published SDK 0.3.150 API — likely `systemPrompt` or `appendSystemPrompt`). The SDK then includes it on every turn as a persistent system message, not just turn 1.
4. Sidecar logs the initial_prompt's first 80 chars + total length on startup so operators can verify the wiring landed.

The "include on every turn" semantic matches what `CLAUDE.md` does in Claude Code CLI — it's context, not a one-shot greeting. Spec'd as "fired once on first spawn" in the architecture but in practice operators expect persistent charter.

## Alternative interpretation worth considering

If the spec actually wants `initial_prompt` to be **one-shot** (literally a synthetic first user message, not a system prompt), the wiring is different:

- Sidecar receives `SEXTANT_INITIAL_PROMPT`, persists a "seeded" flag on first spawn, on first SDK call prepends the initial_prompt as a user message before the operator's actual first prompt.
- On subsequent turns and restarts, skip.
- Architecturally fragile because the "first turn" is hard to define after `--preserve-session` restart.

Lean against this — system-prompt semantic is more useful and easier to reason about. Update `specs/architecture.md` §11b to clarify the spec intent if the choice happens.

## Acceptance

`TestInitialPromptReachesSDK`:
1. Template with `initial_prompt = "Your codename is fnord."`
2. Spawn agent (mock-driver to avoid real API call); the mock driver captures the SDK options it was invoked with.
3. Send a turn.
4. Assert the captured SDK options' `systemPrompt` (or equivalent) contains "Your codename is fnord."

Plus an integration smoke (gated behind `ANTHROPIC_API_KEY`-required env) that spawns a real agent with a codename-style initial_prompt and verifies the model reports the codename when asked.

## Related

- `pkg/templates/template.go` (field defined), `pkg/rpc/handlers/spawn.go:198` (read), `pkg/sextantproto/agent.go:34` (persisted), `images/sidecar/entrypoint/src/index.ts` (gap)
- `specs/architecture.md` §11b — template schema. Field documented; semantic doesn't specify system-prompt-vs-first-user-message. Clarify in this PR.
- `[[bug-claude-seed-readonly-breaks-session-persistence]]` — sibling memory-loading defect. Once both ship, the assistant agent gets full pre-loaded context.
- Workaround in current `~/.config/sextant/templates/assistant.toml`: charter inlined; operator has to keep host CLAUDE.md and inline template charter in sync until both this issue and `claude_seed` RO are fixed.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-initial-prompt-not-forwarded-to-sdk.md
Discovered in: assistant-agent first-run smoke
Original created_at: 2026-05-25T14:45-07:00
Fixed in: a39c6ade910441465f06778491fb3d0c604441b8
<!-- SECTION:NOTES:END -->
