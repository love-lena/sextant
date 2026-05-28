---
title: Adopt semver for the sextant binary and protocol; gate all changes through PRs
status: open
priority: P2
created_at: 2026-05-27T19:20-07:00
labels: [process, versioning, governance]
discovered_in: post-lifecycle-truth merge — we shipped a substantial proto change (LifecyclePayload.source field, new lost transition/state) without a version bump anywhere; the daemon and CLI carry no version string at all today
---

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
