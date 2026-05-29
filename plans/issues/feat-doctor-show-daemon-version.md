---
title: Surface daemon + CLI version in `sextant doctor`
status: fixed
priority: P2
created_at: 2026-05-27T19:20-07:00
fixed_in: 28de5f2
labels: [feature, cli, doctor, observability, operator-experience]
discovered_in: post-lifecycle-truth merge — operator had no way to confirm whether the running daemon was the post-merge binary or a stale one still on PATH
---

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
