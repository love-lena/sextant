# `protocol/` — Sextant's source of truth

This directory **is the protocol** — the language-neutral, transport-neutral
definition that the Go SDK, the CLI, the MCP server, and any bring-your-own
client conform to. The SDK is the reference client, not the definition
(**ADR-0017**).

## Layout

| File | What it is |
|---|---|
| `lexicons/*.json` | The wire envelope + the record shapes, in AT-Protocol lexicon format (a minimal subset). The data layer. |
| `methods.json` | The verb index — one entry per domain verb, **transport-neutral** (no backend operations). The API. |
| `semantic-contract.md` | One page: the behaviour any backend must honour (durability, ordering, CAS, replay, identity). |
| `nats-binding.md` | How the NATS backend realises each verb. `pkg/wire` + `pkg/sx` are its Go expression. |

## How to read it

- A **non-Go client author** reads `lexicons/` (what the bytes look like),
  `methods.json` (what the verbs are), and `nats-binding.md` (how to drive NATS
  to perform them). `semantic-contract.md` tells them what "correct" means.
- A **second-backend author** reads `methods.json` + `semantic-contract.md` and
  writes a new binding doc alongside `nats-binding.md`. Layers above stay put
  (ADR-0013): no backend type leaks `methods.json`.

## Two things to know

- **NSID is deferred** (ADR-0017). Lexicon ids here are the *name* of the
  eventual NSID, minus its reverse-DNS *authority* — `chat.message`, not
  `dev.example.chat.message`. Records carry `$type` from day one; only the
  authority gets prepended later (a MAJOR version bump, not an epoch bump).
- **"Topic" not "channel"** for the bus's named rooms (`msg.topic.<name>`).
  "Channel" is reserved for the Claude Code harness mechanism.
