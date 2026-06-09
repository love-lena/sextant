# Go Static Checks — the enforced layer

The mechanical floor. Everything here is enforced by tooling, never by reviewer memory.
It runs identically locally (`make check`, optionally as a pre-commit hook via
`make hooks`) and in CI (CI is authoritative). Companion to the **go-house-style skill**
(`.claude/skills/go-house-style/`), which carries the judgment-based style.
**Principle: if a tool can decide it, a tool enforces it — and the skill never restates
what's gated here.**

---

## Baseline

- **Single Go module** — one `go.mod` at the repo root; libs are packages beneath it.
  Encapsulation is done with `internal/`, not module boundaries.
- **Go version pinned in `go.mod`** — keep it at the latest patch release; govulncheck
  flags reachable stdlib vulnerabilities, and the fix is usually the next patch.
- **Pinned tool versions** so local, CI, and agent runs never disagree: gofumpt and
  govulncheck as `tool` directives in `go.mod` (run with `go tool …`), golangci-lint as a
  versioned `go run` in the Makefile.

## The gate — `make check`, fail-fast order

1. `gofumpt` check (`make fmt-check`)
2. `go mod tidy -diff` (`make tidy-check`)
3. `go build ./...`
4. `golangci-lint run` (`make lint`)
5. `go fix -diff ./...` (modernizer gate, `make fix-check`)
6. `govulncheck` (`make vuln`)
7. `go test ./... -race` (`make test`)

Cheapest / most-frequent failures first; the expensive race suite last. CI runs the same
targets as separate steps (plus `make e2e` at the end) so the failing layer is visible at
a glance, and `-race`/e2e never burn minutes on a tree that fails formatting.

## Linters and what each enforces

The config is `.golangci.yml` (curated allowlist, `default: none` — not the kitchen-sink
default). It runs with `build-tags: [e2e]` so the tagged e2e suite is linted too.

**Formatting & hygiene**
- `gofumpt` — canonical formatting (stricter gofmt superset).
- `go mod tidy -diff` — keeps go.mod/go.sum honest (fails on drift).

**Correctness & safety**
- `govet` — includes Printf-wrapper checks and copylocks.
- `staticcheck` — broad correctness suite.
- `errcheck` with `check-type-assertions: true` — unchecked errors **and** unchecked type
  assertions, which backstops the comma-ok requirement.
- `forcetypeassert` — second guard for comma-ok.
- `ineffassign` — ineffective assignments.
- `bodyclose` — HTTP response bodies closed.
- `gosec` — common security issues. Tuned for a local-first CLI + embedded bus: the
  path-taint rules (G304/G703) are off because every file path here is operator input by
  design, and the permission thresholds are dirs 0755 / files 0644 — secret material
  (seeds, creds) is written 0600 and *asserted by tests* (`pkg/bus/auth_perms_test.go`),
  which is a stronger guard than a blanket lint threshold.
- `unconvert` — unnecessary type conversions.
- `copyloopvar` — loop-variable copies made redundant by Go ≥ 1.22 semantics.

**House-style rules that happen to be mechanizable** (routed here instead of the skill)
- `revive bare-return` — naked returns banned entirely.
- `gochecknoinits` — no `init()`.
- `gochecknoglobals` — no package-level globals. *Caveat: the actual rule is "no mutable
  global state"; this linter also flags idiomatic immutable globals (sentinel
  `var ErrX = errors.New(...)`, lookup tables). Add a config allowance for those rather
  than weakening the rule — the production tree currently needs none.*
- `errorlint` — correct `%w` usage and `errors.Is`/`As` instead of `==` / type switches.
  Enforces the **mechanics** of the wrapping policy and behavior-asserting; the **policy
  choice itself** lives in the skill.
- `containedctx` — no `context.Context` stored in a struct (the "never stored" half of the
  skill's context rule).
- `noctx` — outbound calls (HTTP, exec, sql) built without a context. Test files are
  excluded: tests drive subprocesses synchronously and the harness owns their lifetime.
- `revive` (subset) — receiver-naming consistency, exported-symbol doc comments present,
  package comment present, early-return / indent-error-flow.
- `godot` — doc comments end in a period.

**Deferred — depguard (one-directional imports).** Domain packages must not import infra,
but the layer ruleset can't be written until the layout settles: today `pkg/` and
`internal/` import each other both ways, and [[feat-layout-no-pkg]] owns deciding the
public surface and the import direction. Enable depguard with the real ruleset when that
ticket lands.

**Supply chain**
- `govulncheck` — known-vulnerable dependencies the code actually reaches (separate step,
  exit 3 on findings). For stdlib findings, bump the patch version in `go.mod`.

## Tests

- `go test ./... -race` — the **race gate**. The only thing that catches data races; runs
  the whole suite under the race detector.
- `make e2e` — the tagged acceptance suite (`tests/e2e/`, M2 DoD). Bare `go test ./...`
  stays green without the tag; CI runs both.
- **Coverage is reported, not gated.** Gating coverage reliably produces gamed,
  assertion-free tests; we watch it as a signal instead.

**Build tags.** A build tag is invisible to anything that doesn't pass it, so every tag in
use must be carried in three places or tagged files rot silently: the test run (`make e2e`
/ CI), the linter (`run.build-tags` in `.golangci.yml`), and editor config if the tagged
code should get gopls diagnostics. The `e2e` tag carries the first two today. Any future
tag (e.g. a `testfeatures` internals hatch — see the skill's testing ladder, last rung)
gets all three plus an always-built `t.Skip` stub.

## Agent loop (deferred — to spec separately)

The same checks wired as PostToolUse hooks so the agent self-corrects in-loop, plus the
gopls MCP server for compiler-grade diagnostics. Not yet specced.

## Rule → enforcement map

| Rule (skill section) | Enforced by |
|---|---|
| Comma-ok type assertions (APIs & Interfaces) | errcheck `check-type-assertions`, forcetypeassert |
| Imports point one direction (Packages & Layout) | depguard — *deferred on [[feat-layout-no-pkg]]* |
| Always check errors (Errors) | errcheck |
| Wrapping mechanics — `%w`, Is/As (Errors) | errorlint *(policy → skill)* |
| Assert on behavior, not error type (Errors) | errorlint *(judgment → skill)* |
| ctx never stored in a struct (Concurrency) | containedctx *(rest → skill)* |
| ctx on outbound calls (Concurrency) | noctx *(judgment → skill)* |
| No copying locks (Concurrency) | govet copylocks *(aliasing judgment → skill)* |
| No init() / no global state (APIs & Interfaces) | gochecknoinits, gochecknoglobals |
| Consistent receiver names (Naming) | revive *(judgment → skill)* |
| Early return / low nesting (Errors) | revive early-return, indent-error-flow *(judgment → skill)* |
| No naked returns (Errors) | revive bare-return |
| Printf wrappers end in `f` (APIs) | govet |
| Doc comments on exported syms (Comments & Docs) | revive, godot *(quality → skill)* |
| Package doc comment present (Comments & Docs) | revive *(content → skill)* |

Everything not in this table is judgment a linter can't make — it lives only in the skill.
