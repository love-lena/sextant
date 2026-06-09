# Authoring the reference — what's left to write

This book is built two ways, and the split is deliberate:

- **Generated, factual, don't hand-edit.** Operation tables, lexicon field tables,
  and the Go SDK API reference — all rendered from canon by `cmd/docgen`
  (`make book`), with a CI drift-check. These state only mechanical fact and never
  make a conceptual claim.
- **Prose, hand-written, yours.** The conceptual framing — what the pieces *are*
  and how they relate — lives in the pages below. Each ships as a stub with a
  `🚧 Claude outline — TODO for Lena` banner and a bullet list of suggested
  coverage. The conceptually-loaded pages embed a generated field table via
  `{{#include}}`, so you write the framing and the table fills itself in.

The factual half is merged and live. The prose half is the work; this is the
checklist. Delete each page's banner when it's done.

## The prose pages

| Page | Covers | Done when |
|---|---|---|
| `src/introduction.md` | What Sextant is; the two primitives; who the book is for; how to navigate. | A newcomer knows what Sextant is and where to go, in under a screen. |
| `src/getting-started/install.md` | `sextant up`, the `sx` namespace one-liner, `register --self` → a context, verify with `clients list`. | A reader can stand up a bus and confirm they're connected. |
| `src/getting-started/first-client.md` | Narrative around the runnable quickstart (embedded below the prose). Walk Connect → publish → read → artifact → drain. | The walkthrough explains each step of the included, verified program. |
| `src/protocol/overview.md` | The conceptual frame: actors, record/frame split, the two primitives, call vs request/reply, bus-enforced identity. **Includes the record-is-any-lexicon point** (the document≠artifact distinction). | The reference pages that follow read correctly because this framed them. |
| `src/protocol/lexicons.md` | Intro: a record is any valid lexicon; these are reference types, not the only ones. Prose for `chat.message` and `document`. Tables included. | The page can't be misread as "these are the only record types." |
| `src/protocol/frame.md` | Record = user space / frame = bus space; bus-stamped fields; unforgeable author; kind discriminates. Table included. | A reader knows which fields the client sets (none of the frame) vs the bus. |
| `src/protocol/registry.md` | Registry vs presence (the load-bearing distinction); listed = issued-and-not-retired. Table included. | "Presence" and "the registry" are clearly different things. |
| `src/protocol/connection.md` | Connect handshake, the two credential tiers + `sx` guardrail, issuance, retire vs disconnect vs drain. | A reader understands how identity, auth, and creds work end to end. |
| `src/protocol/epoch.md` | What the epoch is; checked on connect + per frame; what bumps it; what a client does on mismatch. | Short and correct — a reference note, not an essay. |
| `src/sdk-go/overview.md` | What the SDK is; the Connect→Client shape; how the surface maps to operations; lifecycle. (Per-area pages + API reference are generated.) | Orients a Go developer before the generated reference. |

## Not on the list (generated — leave them)

`src/protocol/operations.md`, `src/sdk-go/{reference,messages,artifacts,clients}.md`,
and everything under `docs/book/generated/`.
