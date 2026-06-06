---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Domain Docs

How the engineering skills should consume this repo's domain documentation.
Sextant is **single-context**: one `CONTEXT.md` + `docs/adr/` at the repo root.

## Before exploring, read these
- **`CONTEXT.md`** at the repo root — the shared language (a glossary).
- **`docs/adr/`** — read ADRs that touch the area you're about to work in.

Both exist as of this setup. If a file you expect is missing, proceed silently;
the producer skill (`grill-with-docs`) creates ADRs and glossary terms lazily as
real decisions and terms get resolved.

## File structure

    /
    ├── CONTEXT.md          ← domain glossary
    ├── docs/adr/           ← architectural decisions
    │   ├── 0001-vision.md
    │   └── 0002-documentation-and-process-layout.md
    └── ...

(If Sextant ever splits into independent contexts, switch to a `CONTEXT-MAP.md`
at the root pointing at per-context `CONTEXT.md` files.)

## Use the glossary's vocabulary
When your output names a domain concept (an issue title, a refactor proposal, a
hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to
synonyms the glossary lists under `_Avoid_`.

If a concept isn't in the glossary yet, that's a signal — either you're inventing
language the project doesn't use (reconsider) or there's a real gap (note it for
`grill-with-docs`).

## Flag ADR conflicts
If your output contradicts an existing ADR, surface it explicitly rather than
silently overriding:

> _Contradicts ADR-0002 — but worth reopening because…_
