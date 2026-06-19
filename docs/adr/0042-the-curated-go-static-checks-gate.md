---
status: proposed
date: 2026-06-19
---

# A curated Go static-checks gate, paired with the house-style skill

Sextant is built to be understandable from its parts — the file tree names what
each thing *is*, and a module shows a small surface over substantial hidden work
([ADR-0041](0041-clients-are-co-equal-across-languages.md)). Two layers keep the
Go that fills that tree faithful to it: a **house-style skill** that carries the
judgment a reader brings, and a **curated static-checks gate** that mechanically
holds the floor the skill rests on. This ADR records what the gate is, the
principle that decides its membership, and the five calibration calls that set
its initial shape. It realises the long-open TASK-17 ("adopt golangci-lint on the
rebuild") under TASK-181.

## Two layers, one boundary

The skill (`.claude/skills/go-house-style/`) is the judgment layer: interface
design, the tree-as-architecture layout, deep-module shape, error-handling
policy, concurrency discipline, naming. The gate (`make lint`, the same checks in
the CI Go job) is the enforced floor: formatting, error-checking, `%w`/`Is`/`As`
mechanics, the broad staticcheck suite, and the `importcheck` bright lines. The
boundary between them is sharp and deliberate — **if a tool can decide it, a tool
enforces it, and the skill never restates what the gate already settles** — so
review attention goes to the decisions a linter genuinely cannot make. The
enforced layer is documented for contributors in
[docs/agents/go-static-checks.md](../agents/go-static-checks.md).

## The gate is curated: high-value, low-friction, zero `//nolint` debt

The gate exists to catch real defects cheaply, not to maximise the number of
linters. A rule earns its place by catching real problems **without** fighting a
legitimate Go idiom — that is, without requiring path exclusions or per-line
`//nolint` suppressions to live alongside ordinary, correct code. The whole tree
passes the set clean with **zero `//nolint` directives**, and that is a property
worth protecting: a gate that accrues suppressions teaches contributors to reach
for the suppression, and the floor quietly rots. So the rule is positive and
firm — **a check that cannot run clean against legitimate code becomes a skill
convention (judgment), not a gate (linter).** The skill is the home for the
disciplines a reader must weigh; the gate is the home for the ones a tool can
settle outright.

The curated set, enabled over `default: none` (an allowlist, golangci-lint v2):
`govet`, `errcheck` (with `check-type-assertions`, test-relaxed), `errorlint`,
`ineffassign`, `staticcheck` — plus the custom **`importcheck`** dependency-closure
assertions that enforce the [ADR-0041](0041-clients-are-co-equal-across-languages.md)
strata edges, run as ordinary Go tests in `make lint` and CI. gofumpt formatting
stays its own step.

## The five calibration calls

A prior audit (against the dash TUI) and a fresh sweep of the whole post-172 tree
surfaced five places where a candidate rule met a real idiom. Each was resolved
by the principle above:

1. **`containedctx` → skill convention, not a gate.** The linter flags every
   `context.Context` stored in a struct. But a long-lived process, server, or
   Bubble Tea model legitimately holds the **lifetime** context it was built on —
   the bus server, the dispatch and workflow coordinators, the MCP server's
   connect context, the TUI models — and there is no clean mechanical line between
   those and an accidentally-captured request context. Enforcing it would mean
   path exclusions across the bus and three app daemons plus the TUI tree, turning
   a "curated" rule into a list of carve-outs. The discipline (ctx is the first
   argument, never stored except for a deliberate lifetime context, documented on
   the field) moves to the skill.
2. **`gochecknoglobals` → skill convention, not a gate.** The linter also flags
   idiomatic *immutable* globals — sentinel errors, lookup tables, preset
   orderings — and there is no clean "immutable only" exclusion. The actual rule
   is "no mutable package state," which the skill states as a convention (prefer
   `const`/func; immutable lookup tables are fine; thread mutable state through a
   struct).
3. **No-new-package / the old `pkg/` rule → the tree-as-architecture principle.**
   Post-172 there is no top-level `pkg/` to ban, and "is this package a deep
   module?" has no mechanical test. The skill carries it: don't add a package
   unless it is a deep module with a small surface over substantial hidden work;
   the `importcheck` assertions enforce the *edges* between the strata that do
   exist.
4. **Error-wrapping → `errorlint` for the mechanics, the skill for the policy.**
   `errorlint` enforces correct `%w` and `errors.Is`/`As`. *Whether* to wrap is a
   judgment the skill makes: wrap errors that cross a module boundary with `%w`
   plus the context the caller needs; a bare return is fine within a module, where
   the immediate caller already has the context. (`wrapcheck` was considered and
   declined as too noisy for this policy.)
5. **Test files → `errcheck` relaxed, the rest kept.** `_test.go` is excluded from
   `errcheck` (tests drive subprocesses synchronously and the harness owns their
   lifetime). `errorlint` and `staticcheck` stay on tests — a wrapped-error
   comparison or a real defect is a defect in a test too.

## Forcing the tree clean now, not later

The gate is adopted while the tree is fixable in one pass: every genuine violation
the curated set found — unchecked errors and closes, `%v`-where-`%w`-was-meant, an
unchecked type assertion, a deprecated `parser.ParseDir`, a few staticcheck
simplifications — was **fixed**, not suppressed. The result is a tree that passes
the curated set clean, with zero `//nolint`, and a CI step that keeps it that way.

## Consequences

- `make lint` and the CI Go job gain the curated golangci-lint step; the
  `importcheck` bright lines run in both. gofumpt stays a separate step.
- New code is held to the floor by construction; new strata packages add an
  `imports_test.go` assertion so the dependency edges stay enforced.
- The three calibration disciplines that left the gate (mutable globals, deep
  modules, contained ctx) now live in the skill — judgment a reviewer or an agent
  applies, recorded once so it isn't re-litigated.
- A future linter joins only if it can run clean against the whole tree; one that
  would need suppressions to live with a legitimate idiom belongs in the skill.
- TASK-17 (the original "adopt golangci-lint on the rebuild" ticket) is reconciled
  as realised by this ADR's gate; its five parked calibration questions are the
  five calls recorded above.
