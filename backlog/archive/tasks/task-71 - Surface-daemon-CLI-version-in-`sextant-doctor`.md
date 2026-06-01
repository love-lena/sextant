---
id: TASK-71
title: Surface daemon + CLI version in `sextant doctor`
status: Done
assignee: []
created_date: '2026-05-27 19:20'
labels:
  - feature
  - cli
  - doctor
  - observability
  - operator-experience
  - 'slug:feat-doctor-show-daemon-version'
  - P2
  - 'closed:fixed'
dependencies: []
priority: medium
ordinal: 71000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Summary

`sextant doctor` should print:

- CLI version (the binary running `doctor`)
- Daemon version (queried via a new `get_version` RPC or pulled from
  the existing `runtime.json`)
- Mismatch warning when the two diverge (a stale CLI on a newer
  daemon is the common case after `make install`)

Today the operator's only signal that they're running the right
build is "the new feature appears to work." Lifecycle-truth landed
on `main` but the symptoms suggested the daemon hadn't actually
picked up the new binary — `doctor` should make that diagnosable.

## Fix shape

1. Depends on [[feat-semver-versioning]] for the version string itself.
2. Add `get_version` RPC handler (`pkg/rpc/handlers/version.go`) that
   returns `{daemon_version, proto_version, started_at, pid}`.
3. `cmd/sextant/doctor.go` calls it as part of the existing
   reachability check; prints a row in the report.
4. Mismatch detection: if `cli.Version != daemon.Version` print
   `! CLI 0.2.0 / daemon 0.1.7 — run \`sextant daemon restart\``.

## Acceptance

- `sextant doctor` output includes a `Versions:` section.
- After `make install` without a daemon restart, doctor warns about
  the mismatch.

## Related

- [[feat-semver-versioning]] — the version string this surfaces.
- [[feat-doctor-stale-binary-detection]] — already resolved; this is
  the version-surface complement.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-doctor-show-daemon-version.md
Discovered in: post-lifecycle-truth merge — operator had no way to confirm whether the running daemon was the post-merge binary or a stale one still on PATH
Original created_at: 2026-05-27T19:20-07:00
Fixed in: 28de5f2
<!-- SECTION:NOTES:END -->
