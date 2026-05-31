---
name: backlog
description: >-
  Sextant's ticket system. File, triage, drive, resolve, and navigate bug and
  feature tickets through the `backlog` CLI (Backlog.md). Reach for this
  whenever you need to file a bug or feature, look at the board or backlog,
  check what's open or in-progress, set a ticket's priority or status, mark
  work resolved, or find tickets related to code you're touching — even when
  the request is as terse as "file a ticket", "what's on the board", "is there
  an issue for this?", or "mark that done". This is the ONLY ticket workflow in
  sextant; it replaces the old hand-edited `plans/issues/` markdown. Never
  hand-author or hand-edit ticket files — drive them through `backlog` so the
  board, web UI, and on-disk markdown stay in sync.
---

# Sextant tickets (Backlog.md)

Tickets live in `backlog/` at the repo root and are driven by the `backlog`
CLI. Each ticket is a markdown file with frontmatter; the CLI keeps the
board, the web UI, and the files consistent. This skill is the go-forward
replacement for `plans/issues/` — same philosophy (tickets are
documentation), different machinery.

## The one rule that bites first: `--plain`

Bare `backlog board`, `backlog task list`, and `backlog task <id>` open
interactive TUIs. In an agent context those hang. **Always pass `--plain`**
for anything you need to read as text:

```
backlog task list --plain
backlog task <id> --plain
backlog search "heartbeat" --plain
```

`--plain` is the script/AI output mode. Use the bare (TUI) forms only when an
operator is driving it themselves, or when you deliberately launch the web UI
with `backlog browser`.

## Command map

| You want to… | Command |
|---|---|
| File a ticket | `backlog task create "Title" …` (see below) |
| Read a ticket | `backlog task <id> --plain` |
| List / filter | `backlog task list [-s "<status>"] [-a @who] --plain` |
| Find by keyword/label | `backlog search "<query>" [--priority high] --plain` |
| Pick up work | `backlog task edit <id> -s "In Progress"` |
| Record progress | `backlog task edit <id> --append-notes "…"` |
| Tick an acceptance item | `backlog task edit <id> --check-ac <n>` |
| Resolve | `backlog task edit <id> -s "Done" --final-summary "…"` then `backlog task archive <id>` |
| See the board | `backlog board` (interactive TUI — for an operator, not agent output) |
| Web UI | `backlog browser` |

IDs are referenced as `task-7` or just `7`.

## Filing a ticket

This is the highest-frequency action, and the place sextant's conventions
matter most. A good ticket is readable cold by someone who wasn't in the
conversation that surfaced it — write the *why* alongside the *what*.

```
backlog task create "sextant doctor should detect stale installed binary" \
  -d "After make build + cp, operators forget to reinstall, so a stale binary runs against newer code. Bit us in the wire-up smoke: ~/.local/bin/sextantd predated env-var forwarding, so ANTHROPIC_API_KEY never reached the container." \
  --ac "doctor emits a warn (not fail) when the embedded SHA is an ancestor of HEAD, with 'N commits behind' in the detail" \
  --ac "warn is silent when no SHA is embedded or no repo_root is configured" \
  --plan "Embed git SHA via -ldflags; doctor compares embedded SHA against git rev-parse HEAD in repo_root; warn-only." \
  --priority low \
  -l "feature,doctor,build,ergonomics,slug:feat-doctor-stale-binary-detection,P3" \
  --notes "Discovered in: post-wire-up validation. Related: [[bug-restart-no-api-key-forwarding]], [[feat-make-install-target]]"
```

What each piece carries:

- **Title** — the one-line summary. Keep it specific.
- **`-d` description** — the *why* and the *what's broken*. This is the old
  `## Summary` + `## Proposed fix` narrative. Don't paste a patch; describe
  the fix shape.
- **`--ac` acceptance criteria** — repeatable, one per `--ac`. This is the old
  `## Acceptance` section. Concrete, checkable statements — ideally the test
  that would prove the fix. These become a checklist you tick with
  `--check-ac <n>` as work lands.
- **`--plan`** — the implementation plan / fix shape, when you have one.
- **`--priority`** — `high | medium | low`. See the priority ladder below.
- **`-l` labels** — comma-separated. Always include:
  - the **kind**: `bug` or `feature`,
  - relevant **area** tags (`doctor`, `restart`, `tui`, `ctl`, …),
  - the **slug** as `slug:<slug>` (see "Slugs and cross-links"),
  - the **priority label** `P1` / `P2` / `P3` (mirrors `--priority`, but keeps
    the exact sextant tier — `P1`/`P2`/`P3` carry more meaning than
    high/med/low; see the ladder).
  - `needs-input` (or `needs-<name>`) when the ticket exists to get a human
    decision, not to prescribe one.
- **`--notes`** — the provenance and pointers. Backlog.md doesn't keep
  arbitrary frontmatter, so the old `discovered_in` / `fixed_in` /
  `resolved_at` fields live here as `Discovered in: …`, `Fixed in: <sha>`, and
  the `[[slug]]` cross-links go here (or in `-d`).

## Slugs and cross-links

Sextant tickets have always had a dense, load-bearing cross-reference graph —
it's what makes the corpus navigable months later. Backlog.md identifies tasks
by numeric ID (`task-7`), which means nothing to a human. So slugs stay the
real identity:

- Every ticket carries its slug as a `slug:<slug>` label. Slug naming is
  `<bug|feat>-<area>-<detail>` — `bug-` for defects, `feat-` for missing
  functionality.
- Cross-link other tickets in prose as `[[other-slug]]`, exactly as before.
  It's a documented pointer; it doesn't need to render. **Cross-link
  liberally** — the dependency graph is the navigability.
- To resolve a `[[slug]]` back to a live task, **ripgrep the files** — this is
  the reliable path:

  ```
  rg -l "slug:<slug>" backlog/tasks backlog/archive
  ```

  The matching file is named `task-<N> - ….md`, so the filename gives you the
  ID. Don't reach for `backlog search` here: its index is fuzzy over **titles
  and descriptions only** — it does *not* match labels or hyphenated slug
  tokens, so `backlog search "slug:…"` returns nothing. Tickets are plain
  markdown, so grepping the `slug:` label in frontmatter is bulletproof.
- For relationships that are genuine *sequencing* (this can't start until that
  ships — e.g. ordered milestone work), also wire the native dependency so the
  board renders the edge: `--dep task-3,task-5` on create, or
  `backlog task edit <id> --dep …`.

## Driving a ticket through its life

```
backlog task edit 7 -s "In Progress"               # picking it up
backlog task edit 7 --append-notes "Root cause: …"  # progress, findings
backlog task edit 7 --check-ac 1                     # tick acceptance as it lands
backlog task edit 7 -s "Done" --final-summary "Fixed in <sha>: <one-liner>."
backlog task archive 7                               # closed tickets leave the live board
```

Closed tickets are **archived, not deleted** — they're history, and the
`[[slug]]` graph still points at them. Archiving just clears the live board.
Record *how* it was resolved in `--final-summary` (and the fixing commit in
notes); a future reader should understand the resolution without digging
through git.

`wontfix` → set status `Done`, add a `wontfix` label, archive, and use
`--final-summary` to say *why* it won't be fixed. (We deliberately don't add a
custom "Won't Do" status: it'd add a board column for a handful of tickets, and
Backlog.md mangles status names containing apostrophes. The `wontfix` label
carries the distinction.) `deferred` → leave as `To Do` and add a `deferred`
label.

## Finding things

```
backlog task list -s "To Do" --plain          # everything open
backlog task list -s "In Progress" --plain     # what's being worked
backlog search "restart" --plain               # fuzzy over titles + descriptions
backlog search "auth" --priority high --plain  # narrow by priority
backlog board                                  # operator's visual board (TUI)
```

Before filing, **search first** — if a ticket already exists for the thing
you're about to file, extend it instead of creating a duplicate. Note that
`search` only matches title/description prose; to find a ticket by its `slug:`
label or an area tag, ripgrep the files (`rg -l "<label>" backlog/`) instead.

## What to file (and what not to merge)

File a ticket for anything that degrades the operator or agent experience,
even if small. `--help` glitches, a missing flag, an awkward error message —
none urgent, all friction over time. Cheap to file, easy to defer, valuable
to have written down when someone has 30 minutes for polish.

Also file:

- **Design decisions that need a human.** Label `needs-input` (or
  `needs-<name>`), frame the open questions explicitly, and don't speculate
  about implementation when the decision hasn't been made. The ticket tracks
  the question, not a prescribed answer.
- **Root-cause bugs distinct from their symptoms.** When debugging surfaces
  multiple distinct root causes for one symptom, file separate tickets and
  cross-link them. Resist the umbrella ticket — the fix shapes rarely overlap.
- **Deferred spec items that turn out to be load-bearing.** When a
  "post-MVP" item solves a real problem that hit in practice, file it as a
  proper P2+ feature, not a vague "polish later" note.

## Priority ladder

`--priority` takes `high|medium|low`; the `P1`/`P2`/`P3` label carries the
exact sextant tier. The tiers are semantic, not vibes:

- **P1 (high)** — blocks routine operator workflows or agent operation (auth,
  restart, daemon shutdown). Something a user hits on the normal path and
  can't route around.
- **P2 (medium)** — frequent operator pain, or blocks agent capabilities
  (commits, push, archive). Routinely annoying or capability-limiting.
- **P3 (low)** — ergonomics, observability, deferred-by-design items now ripe
  to ship. Real friction, not blocking.

When unsure between two tiers, pick the higher one — an over-prioritized
ticket gets triaged down cheaply; an under-prioritized one gets lost.

## Tickets are documentation

The reason for all of the above: detailed reasoning at file-creation time pays
off when the ticket is picked up three weeks later. Write what's broken, why
it matters, what the fix shape looks like, what acceptance looks like, and what
related work it touches. If the ticket can't be read cold by someone who
wasn't in the room, it isn't done.
