---
id: TASK-72
title: Adopt semver for the sextant binary and protocol; gate all changes through PRs
status: Done
assignee: []
created_date: '2026-05-27 19:20'
labels:
  - process
  - versioning
  - governance
  - 'slug:feat-semver-versioning'
  - P2
  - 'closed:fixed'
dependencies: []
priority: medium
ordinal: 72000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Decision (2026-05-27)

**Ship semver + PR-only policy together.** Implementation on
branch `feat/semver-versioning` (commit `f5cdd1c`), not yet
pushed/merged at the time of this note.

What landed on the branch:

- `pkg/version/` — package with `Version` and `Commit` vars,
  `dev` / `unknown` defaults for `go run` paths.
- `cmd/sextant/version.go` + `cmd/sextantd/version.go` —
  cobra subcommands printing `<Version> (<Commit>)`.
- `Makefile` — `install`/`build` populate version + commit via
  `-ldflags`. Sources VERSION from a top-level `VERSION` file
  and COMMIT from `git rev-parse --short HEAD`.
- `VERSION` file at repo root — content `v0.2.0`. Justified by
  the lifecycle-truth refactor (PRs #2/#3) + four-verb resource
  rename (PR #12) shipping after the previous untagged baseline.
- `pkg/sextantproto/doc.go::ProtoVersion` — pinned to `0.2.0`.
- TS client (`clients/typescript/src/envelope.ts`) and tests
  realigned to the new PROTO_VERSION constant.
- `CLAUDE.md` — added "Versioning + PR policy" section.
- `conventions/git-workflow.md` — added "PR-only policy"
  subsection.

Out of scope and tracked separately:

- `feat-doctor-show-daemon-version` — `sextant doctor` consumes
  the version this provides (next ticket to dispatch).
- Wire-format negotiation (CLI ↔ daemon compat) — separate
  ticket worth filing now that the version strings exist. See
  follow-up note below.
- Release-tag artifact-publish GitHub Action — defer.

Will flip status to `fixed` after the PR merges; the
`feat-semver-versioning` branch is ready to push.

## Follow-up to file

The semver agent flagged a tighter framing worth tracking:
**what happens when `VERSION` and `ProtoVersion` diverge?**
Today they're aligned by hand. A test-time check (e.g.
`assert VERSION_file_starts_with(ProtoVersion)`) or a deliberate
"they diverged at v0.X" rule in `doc.go` would catch the drift
the first time it happens. Pairs naturally with the
wire-format-negotiation work.

## Summary

Sextant has no version surface. `sextant version` doesn't exist; the
daemon log doesn't print one; the proto wire format has no version
field on envelopes either. As the codebase stabilizes this becomes
operator-unfriendly:

- "what version is my daemon" has no answer
- a stale CLI talking to a newer daemon (or vice versa) silently
  hits weird errors instead of a clean compat refusal
- changelog/release-notes have no anchor
- agents shipped with an old SDK won't know to expect new payload
  fields like `LifecyclePayload.source`

Pair this with a process change: **all changes land via PR**. The
recent direct-to-main commits (the brainstorm spec + plan that
preceded the lifecycle-truth PR) bypassed the PR review path. Going
forward, even doc-only changes go through a PR, even if it's a
fast-merge.

## Scope

- Add `cmd/sextant/version.go` and `cmd/sextantd/version.go` — both
  surface `sextant version` / `sextantd version` printing
  `<major>.<minor>.<patch>` plus the git SHA via `-ldflags`.
- Make `make install` and `make build` populate the version via
  `-ldflags "-X github.com/love-lena/sextant/pkg/version.Version=..."`.
- Pin the proto version in `pkg/sextantproto/envelope.go::ProtoVersion`
  to match (it already exists but isn't tied to anything).
- Add a tag-based release flow: tagging `v0.X.0` triggers a GitHub
  Action that builds artifacts. Defer the artifact-publish part to a
  follow-up if it's heavy.
- Document the PR-only policy in `CLAUDE.md` and
  `conventions/git-workflow.md`.

## Out of scope

- Pre-built binaries / homebrew tap — separate follow-up.
- Wire-format negotiation (CLI ↔ daemon) — separate, depends on this.

## Related

- [[feat-doctor-show-daemon-version]] — sibling ticket; doctor reads
  the version this provides.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-semver-versioning.md
Discovered in: post-lifecycle-truth merge — we shipped a substantial proto change (LifecyclePayload.source field, new lost transition/state) without a version bump anywhere; the daemon and CLI carry no version string at all today
Original created_at: 2026-05-27T19:20-07:00
Fixed in: c6d3573
<!-- SECTION:NOTES:END -->
