---
title: `sextant doctor` should detect installed-binary-older-than-HEAD
status: open
priority: P3
created_at: 2026-05-24T23:18-07:00
labels: [feature, doctor, build, ergonomics]
discovered_in: post-wire-up validation (installed binary was stale from before commits)
---

## Summary

After a `make build && cp bin/* ~/.local/bin/` cycle, operators sometimes forget to re-install when new commits land. The result is a stale binary running against newer code expectations. This bit us during the wire-up smoke run — `~/.local/bin/sextantd` was from before the env-var-forwarding commit (`f796467`), so `ANTHROPIC_API_KEY` never reached the container and the SDK failed with "Not logged in" despite the source code having the fix.

## Proposed fix

1. Embed git SHA at build time via `-ldflags "-X github.com/love-lena/sextant-initial/pkg/version.GitSHA=$(git rev-parse HEAD)"`.
2. `sextant doctor` reads the embedded SHA and, when the workspace root is detectable (e.g. via a `--workspace` flag or by walking up from the binary's location, or via a config-file entry), compares against the workspace's `git rev-parse HEAD`.
3. Emits a `warn` check (not `fail`) if installed binary's SHA is not a direct ancestor of workspace HEAD: `installed binary is N commits behind workspace HEAD; consider `make install``.

## Acceptance

`TestDoctorFlagsStaleBinary`: stub git workspace at a known SHA; build binary with old SHA via ldflags; run `sextant doctor`; assert the version check returns `warn` with `behind` in the detail.

## Related

- This issue surfaced during the post-wire-up smoke when stale `~/.local/bin/sextantd` silently missed `ANTHROPIC_API_KEY` forwarding (see [[bug-restart-no-api-key-forwarding]] — those tests should also live behind a stale-binary-detection safety net)
- [[feat-make-install-target]] (related — the install target makes "I forgot" less likely; doctor detection catches when it still happens)
