# Go style — sextant

Goal: **boring, consistent, predictable code**. Every contributor — human or agent — produces code that reads like every other piece of sextant. Cleverness costs review time; consistency saves it.

## Baseline

Adopt **[Uber's Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)** as the baseline. Where this document is silent, Uber's rules apply. Where this document contradicts Uber, this document wins.

## Hard rules

These are enforced by lint or review. Violations block merge.

### Formatting & imports

- `gofumpt` over `gofmt`. Configured in `.golangci.yml`.
- Imports grouped: stdlib, third-party, internal. `goimports` enforces.
- Line length: no hard cap, but prefer breaking at ~100 chars for readability.

### Naming

- Package names are short, lowercase, no underscores. `clickhouseboot` not `clickhouse_boot`.
- Exported names: `CamelCase`. Unexported: `camelCase`.
- Acronyms: keep case consistent. `URLParser` not `UrlParser`; `httpClient` not `hTTPClient` (Uber rule).
- Avoid `Get` prefix on getters: `Agent.Status()` not `Agent.GetStatus()`.
- Error variables prefixed `Err`: `ErrAgentNotFound`.

### Errors

- **Always wrap errors with context** using `%w`: `fmt.Errorf("loading config from %s: %w", path, err)`.
- Use `errors.Is` / `errors.As` for inspection. Don't compare error strings.
- Define sentinel errors as exported vars in the package they originate from.
- Functions that return errors **never return a usable value plus an error** — return `(nil, err)` or `(zero, err)`, not `(partial, err)`.

### Nil safety

- **Prefer value types over pointer types** wherever possible. Values can't be nil.
- **"Zero value is useful"** — design types so the zero value is a valid initial state.
- **Constructor pattern is mandatory** for types where the zero value is invalid: `func NewFoo(...) (*Foo, error)`. No bare `&Foo{}` literals for such types.
- **Fail fast**: return error rather than nil result. Nil-result-no-error is forbidden.
- **Slices instead of pointer-to-slice**: `[]T` has safe nil behavior (`len(nil) == 0`, `range nil` is fine).
- **Never check `if x == nil` on an interface returned from a function** without also considering whether the underlying value is nil. Prefer explicit `(value, ok)` patterns.
- `nilaway` lint runs in CI in fail-build mode. False positives get `//nolint:nilaway // <reason>` with an explicit justification.

### Concurrency

- Channels owned by the goroutine that writes to them. No "anonymous fire-and-forget" goroutines without explicit lifetime tracking.
- `context.Context` is the **first argument** to every function that can block on I/O or that should be cancellable.
- Don't use `select` with a single case — just call the operation directly.
- Use `errgroup` (from `golang.org/x/sync/errgroup`) for coordinating concurrent operations with error propagation.
- Avoid `sync.Mutex` embedded in exported structs; prefer composition with an unexported `mu` field.

### Interfaces

- **Small interfaces** (1–3 methods). Big interfaces are a code smell.
- **Defined on the consumer side** — packages that use an abstraction declare the interface they need. Producer packages return concrete types.
- One interface should describe one role. Compose multiple small interfaces; don't define mega-interfaces.

### Avoid

- Naked returns (no `return` without explicit values in functions with named returns).
- Multi-name variable declarations like `var x, y int = 1, 2`. One declaration per line.
- `init()` functions. Almost always you want explicit initialization in a constructor or `main()`.
- `panic` outside of `main()` and irrecoverable invariant violations. Library code returns errors.
- `interface{}` / `any` — use generics or concrete types. The only valid `any` is for serialization at boundaries.

### Generics

- Use sparingly. Generics are tempting but cost readability. Default to concrete types until you've duplicated a pattern 3+ times.
- Type parameters should be named: `[T Comparable]`, not `[T any]` unless truly unconstrained.

## Linting

Two tools, one make target. CI runs `make lint test` on every commit; failure blocks merge.

- **`golangci-lint`** (v2) for the bulk of checks. `.golangci.yml` enables: `govet`, `staticcheck`, `errcheck`, `gosec`, `revive`, `gocritic`, `unused`, `ineffassign`, `errorlint`, `bodyclose`, `contextcheck`, `copyloopvar`. Formatters: `gofumpt`, `goimports` (local prefix `github.com/love-lena/sextant-initial`).
- **`nilaway`** runs as a standalone tool — golangci-lint v2 does not bundle it. Invoked via `make lint-nilaway` with `-include-pkgs=github.com/love-lena/sextant-initial`. Fail-build mode: any nilaway diagnostic fails the build. False positives get `//nolint:nilaway // <reason>` with an explicit justification.

The exact `.golangci.yml` and `Makefile` live at the repo root.

## What "boring" looks like

| Pattern | Boring | Clever |
|---|---|---|
| Error context | `fmt.Errorf("loading config: %w", err)` | `err` |
| Constructor | `NewClient(cfg Config) (*Client, error)` | `Client{}` then late init |
| Nil safety | `if cfg == nil { return ErrNoConfig }` at boundary | Late-check inside the function |
| Concurrency | `errgroup.WithContext(ctx)` for parallel work | Bare goroutines + channels |
| Iteration | `for _, item := range items` | Index-based loops unless index is used |
| Validation | At constructor / boundary | Sprinkled throughout |

Boring wins.

## Open questions

- Do we adopt `errors.Join` (Go 1.20+) for multi-error reporting? Lean yes.
- Do we use `slog` for logging or stick with structured logs published to the bus? Probably bus is primary; `slog` for framework noise that doesn't deserve a domain event.
- Do we use a typed-options package like `github.com/samber/mo` for `Result[T, E]`? Lean no — non-idiomatic Go, costs readability.
