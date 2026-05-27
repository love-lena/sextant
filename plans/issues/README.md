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
- Cross-reference other issues with `[[other-slug]]`. Doesn't need to render — it's a documented pointer. Cross-link liberally; the dependency graph is what makes the issue corpus navigable later.
- Don't include patches inline; describe the fix shape and the acceptance test.
- When fixed: edit the frontmatter `status` to `fixed` and add `fixed_in: <sha>`. Don't delete the file — it's history.
- When resolved (large multi-commit work, not a single fix): set `status: resolved`, add `resolved_at:`, and prepend a short `## Resolution` section to the body with a pointer to the implementation plan and branch. Cross-link any follow-up tickets that were filed during the work.

## What to file

File a ticket for anything that degrades the operator experience,
even if it's small. The `--help` glitches, the missing `--duration`
flag, the awkward error message — none are urgent, all add friction
over time. Cheap to file, easy to defer, valuable to have written
down when someone has 30 minutes to chip away at the polish backlog.

Also file:

- **Design decisions that need a human's input.** Use the
  `needs-input` label (or `needs-<name>` if it's a specific person's
  call) and frame the open questions explicitly. Don't speculate
  about implementation when the decision hasn't been made — the
  ticket exists to track the question, not to prescribe.
- **Root-cause bugs distinct from their symptoms.** When debugging
  surfaces multiple distinct root causes for the same symptom, file
  separate tickets and cross-link. Resist the urge to merge into one
  umbrella ticket; the fix shapes rarely overlap.
- **Spec items deferred during MVP that turn out to be load-bearing
  in practice.** When a "Deferred (post-MVP)" item solves a real
  operator problem that hit during testing, file it as a P2+ feature
  — not as a vague "polish later" note.

## Tickets are documentation

Detailed reasoning at file-creation time pays off when the ticket is
picked up three weeks later. Write the "why" alongside the "what":
what's broken, why it matters, what the fix shape should look like,
what acceptance looks like, what related work it touches. The
ticket should be readable cold by someone who wasn't in the
conversation that surfaced the issue.
