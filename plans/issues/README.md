# Issues

Known follow-up work surfaced after phase 1 + the SDK wire-up smoke. One file per issue; filename slug describes the thing being fixed. Frontmatter is the metadata; the body explains what's broken and how to fix it.

## Frontmatter schema

```yaml
---
title:          One-line summary
status:         open | in-progress | fixed | wontfix
priority:       P1 | P2 | P3
created_at:     ISO-8601 timestamp with timezone
labels:         [bug | feature, area-tags...]
discovered_in:  short context (e.g. "post-wire-up smoke")
fixed_in:       commit SHA (only when status=fixed)
---
```

## Priorities

- **P1** — blocks routine operator workflows or agent operation (auth, restart, daemon shutdown)
- **P2** — frequent operator pain or blocks agent capabilities (commits, push, archive)
- **P3** — ergonomics, observability, deferred-by-design items now ripe to ship

## Conventions

- Slug naming: `<bug|feat>-<area>-<detail>.md`. `bug-` for defects, `feat-` for missing functionality (spec'd but unbuilt, or missing capability the operator hit).
- Cross-reference other issues with `[[other-slug]]`. Doesn't need to render — it's a documented pointer.
- Don't include patches inline; describe the fix shape and the acceptance test.
- When fixed: edit the frontmatter `status` to `fixed` and add `fixed_in: <sha>`. Don't delete the file — it's history.
