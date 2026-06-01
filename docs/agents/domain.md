# Domain Docs

How the engineering skills should consume this repo's domain documentation when
exploring the codebase. sextant is **single-context**: one `CONTEXT.md` +
`docs/adr/` at the repo root (neither exists yet — see below).

## Before exploring, read these

- **`CONTEXT.md`** at the repo root, and
- **`docs/adr/`** — read ADRs that touch the area you're about to work in.

If any of these files don't exist, **proceed silently**. Don't flag their absence;
don't suggest creating them upfront. The producer skill (`grill-with-docs`) creates
them lazily when terms or decisions actually get resolved. As of this setup, sextant
has neither — that's expected.

## File structure

Single-context repo:

```
/
├── CONTEXT.md          ← domain glossary (not yet created)
├── docs/adr/           ← architectural decisions (not yet created)
│   └── 0001-....md
└── ...
```

(If sextant ever splits into independent contexts, switch to a `CONTEXT-MAP.md`
at the root pointing at per-context `CONTEXT.md` files, and re-run the setup skill.)

## Use the glossary's vocabulary

When your output names a domain concept (an issue title, a refactor proposal, a
hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to
synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal — either you're
inventing language the project doesn't use (reconsider) or there's a real gap (note
it for `grill-with-docs`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than
silently overriding:

> _Contradicts ADR-0007 — but worth reopening because…_
