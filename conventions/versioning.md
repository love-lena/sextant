# Versioning in sextant

The short version lives in `CLAUDE.md` § "Versioning + PR policy".
This is the full model and the reasoning behind it. Read it before
bumping a version or cutting a release.

## The one idea

A version number is a **contract with a consumer**: "will your stuff
break if you upgrade?" The number is meaningless until you've named
*whose* stuff. sextant has more than one consumer, so it has more than
one version line, and they do not move together.

## The four version surfaces

| Surface | Where | Contract is with… | Bumps when… |
|---------|-------|-------------------|-------------|
| **Binary semver** | `VERSION` → `pkg/version.Version` (via `-ldflags`); shown by `sextant version` / `sextantd version` | **Operators + scripts** driving the CLI | a verb, flag, output format, exit code, or default behavior changes |
| **Proto version** | `sextantproto.ProtoVersion` + TS `PROTO_VERSION` | **Protocol peers** on the bus (envelope + RPC wire shapes) | a wire shape changes (new/removed/changed RPC, envelope field) |
| **TS client library** | `clients/typescript/package.json` `version` | **Importers of `@sextant/client`** | the client's exported types/functions change |
| **Sidecar self-report** | the version string in `images/sidecar/entrypoint` | diagnostics only (not a real contract) | should be sourced from the build, not hand-edited |

The **binary semver is the headline number** — it's what a human types
and what a cron job pins. When someone says "what version is sextant,"
they mean `VERSION`.

> **Current reality (2026-05): partially coupled, partially stale.**
> Proto currently *tracks* the binary number by convention. The TS
> library version (`0.1.0`) has drifted and doesn't move with releases.
> The sidecar string is hand-edited and stale. The target is four
> independently-bumped lines; the decoupling work is tracked in
> `slug:feat-split-version-lines`. Until that lands, bump
> proto alongside the binary on a release cut and note the wire delta
> in the changelog.

## Classifying a bump: observability, not diff size

The honest test for the **binary** semver:

> Does someone who only runs the binary notice — and does it break a
> working invocation or a script's assumptions?

- **MAJOR** — a working command stops working or silently changes
  meaning: verb/flag removed or renamed (without an alias), default
  output format changed, exit-code meaning changed, a default behavior
  flipped.
- **MINOR** — purely additive: new verb, new flag, new optional output
  field. The old invocation still does exactly what it did.
- **PATCH** — a bug fix where the *intended* behavior didn't change;
  reality was made to match intent.

Diff size is noise; observability is signal. A 2000-line internal
refactor with zero observable change is PATCH or no bump. A one-char
change to a default flag value is MAJOR.

Each *other* surface uses the same shape against *its own* consumer:
the proto line goes MAJOR only when a peer on the old wire format
breaks; the TS library line goes MAJOR only when an importer's code
breaks. A change can be a non-event for one and a break for another —
that's the whole reason they're separate numbers.

### Worked examples

- **`typescript` 5.6 → 6.0 in the build deps** → not even a binary
  bump on its own. It's build tooling, not the operator contract. (It
  rode along in the v0.3.0 changelog under Changed, but didn't drive
  the version.)
- **Verb migration `spawn → create` with `spawn` kept as an alias** →
  MINOR, not MAJOR. The alias keeps the old invocation working, so the
  change is additive. See "Aliases" below.
- **New `get_version` RPC + new optional `session_log` field** →
  additive on the wire → MINOR-equivalent proto change, informational
  bump. No peer breaks.

## Aliases turn a MAJOR into a MINOR — use them

A rename *with* a deprecation alias is additive (MINOR); a rename
*without* one is a break (MAJOR). Buying a release of overlap for the
cost of an alias is almost always worth it. Deprecate loudly (changelog
`Deprecated` section + a runtime notice), keep the alias for at least
one release, and let the eventual *removal* be the MAJOR. The verb
migration (`spawn`→`create`, `kill`→`stop`, `audit query`→`audit list`,
`worktree destroy`→`worktree delete`) is the canonical example.

## The release-cut workflow

Releases are **cut**, not accrued one PR at a time.

**During normal development — every feature/fix PR:**
1. Make the change under a shipping path.
2. Add a `CHANGELOG.md` entry under `## [Unreleased]` in the right
   subsection (Added / Changed / Deprecated / Removed / Fixed /
   Security). This is CI-gated (`changelog entry required`).
3. **Do not touch `VERSION`.** Leave it alone.

This is why per-PR bumps are wrong here: with N PRs in flight, `VERSION`
is a guaranteed conflict on every one. You'd either serialize merges
(killing parallelism) or fight conflicts on a file whose only content
is a number. `[Unreleased]` exists precisely to hold described-but-
unreleased changes. CHANGELOG entries live on different lines, so they
rarely conflict.

**When it's time to release — a dedicated `release: cut vX.Y.Z` PR:**
1. **Derive the bump from the accumulated `[Unreleased]` entries** (the
   rule at the top of this doc: Removed → MAJOR; else Added → MINOR;
   else PATCH). You read the bump off the changelog; you don't pick it.
2. Bump `VERSION`.
3. Bump `ProtoVersion` (`pkg/sextantproto/doc.go`) + TS `PROTO_VERSION`
   if the wire changed this window; note the delta in the changelog
   `Changed` section. (Until the version-line split lands, keep proto
   tracking the binary number.)
4. In `CHANGELOG.md`: rename `## [Unreleased]` → `## [X.Y.Z] — DATE`,
   add a fresh empty `## [Unreleased]` above it, and fix the link refs
   at the bottom (`[Unreleased]` compare → `vX.Y.Z...HEAD`; add
   `[X.Y.Z]` compare → `vPREV...vX.Y.Z`).
5. Run codegen (`cd clients/typescript && npm run codegen`) to confirm
   no schema drift; build + test.
6. Open the PR, let CI pass, merge.
7. **After merge:** pull `main`, create an annotated tag `vX.Y.Z` on the
   merge commit, push it, and create the GitHub release (so the
   `[X.Y.Z]` changelog link resolves). The tag and release are part of
   the cut — a changelog section with no tag is an unfinished release.

No CI change is needed for any of this: the `changelog entry required`
gate just checks that `CHANGELOG.md` was edited, which is true for both
feature PRs (they add an `[Unreleased]` entry) and the release PR (it
moves entries + bumps `VERSION`).

## The 0.x escape hatch, and when to leave it

Pre-1.0, semver licenses anything to break anytime — which means the
number communicates *nothing* to an operator. sextant is already more
careful than 0.x permits (breaking verb changes ship behind aliases),
which is the sign it has outgrown the excuse.

**Cut 1.0 the moment there is a real operator who is not the author.**
1.0 is a *social* signal — "I will now tell you the truth about breakage
via the version number" — not a statement that the software is done.
Staying on 0.x indefinitely is how a project dodges that commitment
while still expecting people to depend on it. Don't.

While still on 0.x: the middle digit loses its breaking-change meaning
(a `0.x` minor *may* break), but the **patch-vs-minor distinction still
holds** — fixes bump patch, additions bump minor. Don't ship features
under a patch bump; it tells operators "nothing new, ignore this" when
something new exists.

## What does NOT get a bump (or a changelog entry)

Metadata-only changes: `docs/**`, `plans/**`, `conventions/**`,
`.github/**`, `.claude/**`, `tests/visual/**`, root `*.md`, and pure
`*_test.go` changes. Tests describe behavior but don't ship; docs and
specs aren't the operator contract. These can merge without a changelog
entry and never move a version line.
