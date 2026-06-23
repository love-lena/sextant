# Go Static Checks — the curated gate

The mechanical floor. Everything here is enforced by tooling, never by reviewer
memory. It runs identically locally (`make lint`) and in CI (the CI Go job is
authoritative). Companion to the **go-house-style skill**
(`.claude/skills/go-house-style/`), which carries the judgment-based style.

**Principle:** the gate is *curated* — high-value, low-friction, **zero `//nolint`
debt**. A rule earns a place only if it catches real defects without fighting a
legitimate Go idiom. A rule that *would* require path exclusions or per-line
`//nolint` to coexist with an idiom is dropped to the skill as a **convention**
(judgment) instead — the skill names which ones and why. The decision and its
calibration are recorded in
[ADR-0042](../adr/0042-the-curated-go-static-checks-gate.md).

---

## The gate — `make lint`

`make lint` runs, in order:

1. `go vet ./...` — Printf-wrapper checks, copylocks, the standard vet suite.
2. `golangci-lint run ./...` — the curated linter set (`.golangci.yml`).
3. `gofumpt -l .` — the canonical formatting check (stricter gofmt superset;
   `make fmt` rewrites). gofumpt is its **own** step, not a golangci formatter.
4. `go test` of the **import-discipline** packages — the `importcheck`
   bright-line assertions ([internal/importcheck](../../internal/importcheck/importcheck.go)),
   run as ordinary tests in the strata packages.

The CI Go job runs the same pieces as separate steps (`go vet`, `golangci-lint`,
`gofumpt`, then `go test ./... -race`, then the `e2e` suite), so the failing
layer is visible at a glance and `-race`/e2e never burn minutes on a tree that
fails a cheaper check. The import-discipline assertions run inside CI's
`go test ./... -race` step.

## Tooling

- **golangci-lint v2** — `.golangci.yml` uses the **v2 config schema**
  (`version: "2"`). Install with `brew install golangci-lint` (or the v2
  release); CI pins the action to the same v2 line.
- **gofumpt** — `go install mvdan.cc/gofumpt@latest`.

## The curated linter set (`.golangci.yml`)

`default: none` — an allowlist, not the kitchen-sink default. It runs with
`build-tags: [e2e]` so the tagged e2e suite is linted too (a build tag is
invisible to anything that doesn't pass it, so tagged files would rot silently
outside the gate).

| Linter | Enforces |
|---|---|
| `govet` | Printf-wrapper checks, copylocks, the standard vet suite. |
| `errcheck` (`check-type-assertions: true`) | Unchecked errors **and** unchecked type assertions (the comma-ok backstop). |
| `errorlint` | Correct `%w` usage + `errors.Is`/`As` instead of `==` / type switches — the **mechanics** of the wrapping policy; the policy choice lives in the skill. |
| `ineffassign` | Ineffective assignments. |
| `staticcheck` | The broad correctness + simplification suite. |

Plus the custom **`importcheck`** bright lines (run as Go tests, not a golangci
linter): the production dependency closures of the bus, the SDK-facing TUI
strata, and the convention libraries — the edges of
[ADR-0041](../adr/0041-clients-are-co-equal-across-languages.md).

### Exclusions

- **`_test.go` is relaxed for `errcheck`** — tests drive subprocesses
  synchronously and the harness owns their lifetime. `errorlint` and
  `staticcheck` stay on tests (a wrapped-error comparison or a real bug is a bug
  in a test too).

There are **no** path-scoped `//nolint`-style carve-outs: where a rule needed one
to coexist with a legitimate idiom, the rule was dropped to the skill instead
(below).

## Dropped to the skill — conventions, not linters

These were considered and deliberately left to judgment, because enforcing them
mechanically would fight a real Go idiom with no clean exclusion (ADR-0042):

- **No mutable package globals** (was `gochecknoglobals`). The linter also flags
  idiomatic *immutable* globals — sentinel errors, lookup tables, preset
  orderings — and there is no clean "immutable only" exclusion. The skill states
  the rule: prefer `const`/func; immutable lookup tables are fine; no mutable
  package state.
- **No new package unless it's a deep module** (was a `no-new-pkg` / `pkg/` rule).
  Post-172 there is no `pkg/` to ban, and "deep enough" has no mechanical test.
  The skill carries it as the tree-as-architecture / deep-module principle.
- **`context.Context` not stored in a struct** (was `containedctx`). A long-lived
  process, server, or Bubble Tea model legitimately holds its **lifetime**
  context, and there is no clean line between those and an accidentally-captured
  request context. The skill states the rule and its one exception.
- **Error-wrapping policy** — `errorlint` enforces the `%w`/`Is`/`As` mechanics;
  *whether* to wrap (cross a module boundary → wrap with context; within a module
  → a bare return is fine) is the skill's call.

## Tests (CI)

- `go test ./... -race` — the **race gate**, the only thing that catches data
  races; runs the whole suite (including the `importcheck` assertions) under the
  detector.
- `go test -tags e2e ./tests/e2e/` — the tagged acceptance suite (M2 DoD). Bare
  `go test ./...` stays green without the tag; CI runs both.
- **Coverage is reported, not gated.** Gating coverage reliably produces gamed,
  assertion-free tests; we watch it as a signal instead.

**Build tags.** A build tag is invisible to anything that doesn't pass it, so
every tag in use must be carried in the test run (`make` / CI), the linter
(`run.build-tags` in `.golangci.yml`), and editor config if the tagged code
should get gopls diagnostics. The `e2e` tag carries the first two today. Any
future tag (e.g. a `testfeatures` internals hatch — see the skill's testing
ladder, last rung) gets all three plus an always-built `t.Skip` stub.
