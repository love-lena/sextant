---
name: release
description: Cut a new sextant release end to end — pick the version, pre-flight main, prepare the annotated tag for the operator to push (the trusted-path step), watch the release workflow build + publish the tarballs and the Homebrew formula-bump PR, then verify the upgrade is live on the operator's setup. Use when the operator wants to ship a release, "cut a release", "tag a version", "do a release", "release vX.Y.Z", or asks what's needed to get merged work onto their live setup for real (as opposed to an rc / `/rc install`, which is the unreleased fast-lane). Honors the discipline that release tags + the formula merge are human, trusted-path actions — this skill prepares and drives everything around them but never pushes a tag or merges the formula PR itself.
---

# release — ship a new sextant version

A release is triggered by pushing a `vX.Y.Z` tag. `.github/workflows/release.yml`
then builds the per-platform tarballs (binaries + the Claude Code plugin), publishes
a GitHub release with auto-generated notes, and — for a non-prerelease tag — opens a
**Homebrew formula-bump PR** so `brew upgrade` tracks the release. The operator's CLI
updates only through this path (never `make install`; the live-sextant-via-release
discipline).

**You — the agent — drive everything EXCEPT the two trusted-path actions:**
**pushing the tag** and **merging the formula-bump PR**. The auto-mode classifier
blocks an agent from those (they're production pushes); you prepare the exact
commands and the operator runs/clicks them. Everything else — pre-flight, watching
the workflow, finding + checking the bump PR, verifying the live upgrade — is yours.

## Versioning

Semver `vMAJOR.MINOR.PATCH`:
- **patch** (`v0.6.0`→`v0.6.1`) — bug fixes only.
- **minor** (`v0.6.0`→`v0.7.0`) — backward-compatible features (the usual case).
- **major** — breaking protocol/API changes.
- **prerelease** — append `-rc.N` (`v0.7.0-rc.1`). A hyphen makes `release.yml`
  publish it flagged (never "latest") and **skip the Homebrew bump** — for trying a
  release shape without moving everyone's `brew upgrade`. (For trying *unreleased*
  work live, prefer the lighter `/rc` skill instead of an rc tag.)

Confirm the bump with the operator before tagging; don't guess major-vs-minor.

## Steps

### 1. Pre-flight (you)
- `git -C <repo> fetch origin` and confirm **main is current**: the release tags the
  tip of `origin/main`. The operator's primary checkout stays on main; do read-only
  checks there or in a worktree.
- **CI is green on the commit you'll tag** — `gh run list --branch main --limit 1`
  (or `gh pr checks` on the just-merged PR). Never tag red main.
- The tag is unused: `git tag --list <tag>` empty and `git ls-remote --tags origin <tag>` empty.
- **Dry-run the build before tagging** — `scripts/release.sh <tag>` locally (it builds
  every binary for all four platforms + the plugin and stamps the version). This is
  the cheapest place to catch a packaging break (a missing binary in the map, a UI
  build failure); the workflow does the same on the tag. Then `rm -rf dist`.
- **Plugin text channel:** if `clients/claude-code/**` changed since the last release
  (`git diff --stat <last-tag>..origin/main -- clients/claude-code/`), bump
  `clients/claude-code/.claude-plugin/plugin.json` `version` and land it on main FIRST
  (its own small PR) — that's the channel `claude plugin update` reads (separate from
  the binary release; see the plugin-update discipline). Skip if unchanged.
- Draft the release notes mentally from the merged PRs since the last tag (the
  workflow's `--generate-notes` writes them from PR titles, so good PR titles = good
  notes; nothing to hand-write).

### 2. Cut the tag — OPERATOR, trusted-path
Prepare the exact annotated-tag commands and hand them to the operator (the `!`
prefix runs them in-session so you see the result); the classifier blocks you from
pushing a `v*` tag yourself:

```
! git -C <repo> tag -a <tag> -m "<one-line summary of the release>"
! git -C <repo> push origin <tag>
```

(Or the operator adds a Bash allow-rule for `git push origin v*` to let you push it.)
Pushing the tag is the point of no return — it publishes. Make sure pre-flight passed.

### 3. Watch the release workflow (you)
- `gh run watch $(gh run list --workflow=release.yml --limit 1 --json databaseId -q '.[0].databaseId')`
  — or poll `gh run list --workflow=release.yml`.
- Confirm: tarballs built, the **linux smoke** passed (version stamped `sextant <tag> (...)`),
  and `gh release view <tag>` shows the published release with all four
  `sextant_<tag>_<os>_<arch>.tar.gz` assets. A prerelease is flagged.

### 4. Homebrew formula-bump PR (you find + check; OPERATOR merges)
- Non-prerelease only (a prerelease skips it). The workflow opens
  `chore(release): Homebrew formula → <tag>` from branch `homebrew-bump-<tag>`.
- Find it: `gh pr list --search "Homebrew formula" --state open`. Confirm its
  `lint + test (Go)` check is green (it only runs if a `HOMEBREW_BUMP_TOKEN` PAT
  pushed the branch; absent the PAT the PR waits for a manual merge and the check may
  not run — say so).
- **The operator merges it** (a formula→main merge is trusted-path; the classifier
  blocks you). Hand them the one click / `! gh pr merge <branch> --squash`. If the
  formula was edited by hand previously, watch for a tap conflict on the operator's
  next `brew update` (reset the tap with `git -C $(brew --repo love-lena/sextant)
  reset --hard origin/main` if it complains).

### 5. Verify the upgrade is LIVE (you) — the release isn't done until it runs
A release that's published but not running on the operator's machine isn't done
(ACs require end-to-end live operability). After the formula PR merges:
- `brew update && brew upgrade sextant` (this swaps the operator's live CLI to the
  release — the sanctioned path; warn before running, it briefly affects the live
  binaries).
- `sextant version` shows the new `<tag>`.
- Bring up / restart whatever the release changed and confirm it serves — e.g.
  `sextant components restart --all` (or `start dash` for a new component), then
  `sextant components status` shows them healthy. Spot-check the headline feature
  live.

### 6. Post-release (you)
- If the plugin text changed (step 1), tell the operator their sessions pick it up via
  `claude plugin update` + a restart (the dash/MCP/skills channel, distinct from brew).
- Mark the release ticket Done (`backlog`), with a one-line `--final-summary` of what
  shipped and the verified-live confirmation.

## Safety invariants
- **Never push the tag or merge the formula PR yourself.** Both are trusted-path
  production actions the classifier blocks; prepare them and let the operator act.
- **Never tag red main**, and dry-run `scripts/release.sh <tag>` before tagging.
- **Prereleases skip Homebrew** (hyphen in the tag) — don't chase a bump PR that won't
  exist.
- **Verify the version stamp** (`sextant version` == tag) before calling the release
  done — a tag that didn't stamp means the build didn't pick up the ldflags.
- A release ships binaries; **unreleased work for live testing goes through `/rc`**,
  not an rc tag, unless the operator specifically wants a published prerelease.
