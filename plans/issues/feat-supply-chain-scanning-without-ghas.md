---
title: Supply-chain vulnerability scanning that works without GitHub Advanced Security
status: in-progress
priority: P3
created_at: 2026-05-28T12:35-07:00
labels: [feature, ci, security, supply-chain]
discovered_in: PR #17 dropped `actions/dependency-review-action` after it failed with "Dependency review is not supported on this repository. Please ensure that Dependency graph is enabled along with GitHub Advanced Security" — GHAS is a paid add-on not available on private personal-account repos without enterprise
---

## Resolution in progress (2026-05-28)

Shipping the GHAS-free alternatives on branch
`chore/dependabot-and-supply-chain`:

- **`govulncheck` in the Go CI job** — runs against `./...`,
  surfaces called-code + module-level Go vulns. Currently
  `continue-on-error: true` because two `github.com/docker/docker`
  findings (GO-2026-4887, GO-2026-4883) have `Fixed in: N/A` and
  block the gate from flipping. Flip to blocking once upstream
  patches.
- **`npm audit --audit-level=high` in the TS CI job** — blocking
  from day one (local scan returned zero high-severity vulns).
- **`.github/dependabot.yml`** — weekly bumps for `gomod`, `npm`,
  `github-actions`. Patch + minor grouped to avoid PR flood.
  Major bumps still open individually.

Flip status to `resolved` after the PR merges.

## Summary

We tried adding `actions/dependency-review-action@v4` in PR #17 to
catch GHSA-known vulnerable deps on PRs at high/critical severity.
The action calls the dependency-review API, which on private
repos requires **GitHub Advanced Security** — a paid Enterprise
add-on not available on personal accounts. The action failed
deterministically and the workflow was removed.

We still want supply-chain regression catching at PR time.
There are GHAS-free tools that cover the same ground.

## Shape

Add the following CI steps (probably to the existing Go and TS
jobs, not a new workflow — they reuse the toolchain that's
already set up):

### Go: `govulncheck`

[`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
scans Go modules against the
[Go vulnerability database](https://vuln.go.dev/). Already-installed
tool, well-maintained by the Go team.

```yaml
- name: install govulncheck
  run: go install golang.org/x/vuln/cmd/govulncheck@latest

- name: govulncheck
  run: govulncheck ./...
```

Cost: ~5–15 seconds.

### TypeScript: `npm audit --audit-level=high`

Built into npm; no extra install. With workspaces, audits the
entire dep graph from the root.

```yaml
- name: npm audit (workspace root)
  run: npm audit --audit-level=high
```

Cost: ~3 seconds.

### Both jobs

Make the failures advisory rather than blocking at first
(`continue-on-error: true`), observe the noise level for a couple
of weeks, then flip to blocking once we know the false-positive
rate.

## Why P3

Useful but not load-bearing. We landed PR #17 with the other
three CI additions; supply-chain scanning is the deferred fourth.
Both tools are well-established and the wiring is mechanical.

## Acceptance

- `govulncheck ./...` runs in the Go CI job; fails the job if a
  high-severity vulnerability is found (after the observation
  window expires).
- `npm audit --audit-level=high` runs in the TS CI job; fails on
  high-severity findings.
- Both initially gated with `continue-on-error: true` for ~2
  weeks of observation; then flipped to blocking.

## Related

- `[[feat-cli-i-flag-tier1-tier2]]` — separate; supply-chain
  scanning isn't a hard dep for any in-flight ticket.
- PR #17 commit message has the GHAS context that motivated this
  follow-up.
