---
name: go-house-style
description: Authoritative house style for writing Go in sextant. Use whenever writing, reviewing, refactoring, or designing Go code, APIs, or packages — API and interface design, the tree-as-architecture layout, deep-module shape, error-handling policy, concurrency discipline, naming, comments, testing internals. Consult it before any non-trivial Go, even when the task doesn't mention "style." Mechanical, lint-enforced rules live in the curated static-checks gate (make lint), not here; this is the judgment layer for decisions a linter can't make.
---

# Go House Style — the judgment layer

This skill carries the opinions a tool can't enforce. Anything mechanically
checkable (formatting, error-checking, %w mechanics, ineffective assignments,
the broad staticcheck suite) is enforced by the **curated static-checks gate** —
`make lint`, the same gate the CI Go job runs, designed in
[docs/agents/go-static-checks.md](../../../docs/agents/go-static-checks.md) — and
is **not restated here**: don't spend judgment re-deriving what the gate already
decides. The gate is deliberately curated (high-value, low-friction, zero
`//nolint` debt), so a few disciplines a linter *could* mechanically flag live
here instead, because flagging them would fight a legitimate Go idiom with no
clean exclusion (see *Concurrency* and *Packages*). Those are called out below.

## Why this exists — interpretability

Sextant is built to be understandable from its parts, by a human or an agent
reading it cold. That is the through-line of every rule here: code is optimized
for the reader, the file tree names what each thing *is*, and a module shows a
small surface over substantial hidden work so you can reason about it without
reading its insides ([ADR-0041](../../../docs/adr/0041-clients-are-co-equal-across-languages.md)).
Clarity for the next reader is the tie-breaker whenever two readings of "better"
conflict.

## Philosophy

- **Priority order: clarity > simplicity > concision > maintainability > consistency.**
  When two readings of "better" conflict, the higher priority wins — judged through the
  reader's eyes, not the author's.
- **Optimize for the reader, not the author.**
- **Anti-pattern — gratuitous inconsistency:** don't introduce a novel way to do something
  the codebase already does a standard way.
- **Scout's rule:** improve code you pass while working. The risk isn't the size of a
  refactor, it's one that's actually *worse* — judged by the priority order: when you can't
  reasonably argue the existing code is better, fix it up; when it might be, leave it.
- **A little copying is better than a little dependency.**
- **Least mechanism:** reach for the simplest construct that works; stdlib over a framework.
  (This is the style-layer face of the repo's bright lines — primitives not policy, thin
  universal core.)

## APIs & Interfaces

> Guiding principle: **the easy way to use an API should be the correct way.** If the
> obvious call is also the safe call, most of the rest follows.

- **Escalate new or changed interfaces to Lena.** An interface — especially an exported
  one — is among the hardest things to reverse once it has callers, so it's where autonomy
  stops: weigh the alternatives, choose the best design, then flag it in the PR description
  (or the ticket) for review rather than letting it ride in silently. A new or changed
  interface never ships on autonomous judgment alone.
- **Accept interfaces, return concrete structs.** Producers return the concrete type so
  callers get the full API and you can add methods without breaking them. Exception: return
  an interface when genuinely yielding multiple types, or to hide impl in a library.
- **Consumer-defined, small interfaces.** Define the interface where it's *consumed*, not
  beside the implementation; keep it small. Reject the 20-method `Storer` next to its impl.
  (`bus`'s `Backend` is the house example: one interface, defined where the bus consumes
  it, with a conformance suite any implementation can run.)
- **Compile-time interface checks (`var _ Iface = (*T)(nil)`)** — use sparingly, only where
  nothing else exercises the satisfaction.
- **Make the zero value useful** — usable with no constructor where you can manage it.
- **One obvious constructor; functional options for optional config** — so adding a knob
  isn't a breaking signature change.
- **Dependency injection via explicit constructor params; no DI frameworks.**
- **Field names in struct literals.**
- **Avoid boolean parameters**, especially multiple — use options, distinct functions, or a
  config struct.
- **Avoid `interface{}`/`any` in signatures** where generics or a concrete type restores
  type safety. (Lexicon record content is deliberately opaque — `wire.Lexicon` — that's
  protocol design, not an `any` escape hatch.)
- **Consistent error signaling within a package** — `(T, bool)` vs `(T, error)` vs panic,
  matched to severity and applied consistently.
- **Design so the type can't be held wrong** — invalid states unrepresentable; the zero
  value is either safe or fails loudly.
- **Minimal exported surface; unexport by default.**

## Packages, Layout & Modules

The tree is the architecture. The top level reads as what the system *is* —
`protocol/`, `bus/`, `clients/<language>/`, with `tests/` and `docs/` alongside —
not as Go's visibility buckets ([ADR-0041](../../../docs/adr/0041-clients-are-co-equal-across-languages.md)).
Two consequences carry the most weight when you add or move code:

- **No top-level `pkg/`, no `cmd/` bucket.** The pre-172 `pkg/ internal/ cmd/`
  layout is gone; do not reintroduce it. A binary lives under
  `clients/<name>/` (the app *is* a client); a library lives where its
  domain says it belongs. Sort code by what it is, never onto an axis (public
  vs. private, binary vs. library) orthogonal to the design.
- **Deep modules over a tree of shallow packages — the no-new-package rule.**
  *(This is a judgment rule, not a linter: there is no clean mechanical test for
  "deep enough," so it lives here.)* A package earns its place by being a **deep
  module** — a small exported surface hiding substantial complexity (`net/http`;
  `bus`'s `Backend`); a good package hides more than it shows. Don't add a
  package unless it is one. The review question is *how much does this package's
  API hide?* A shallow, pass-through layer doesn't pay for its complexity — fold
  it in. (Same instinct as "abstract only against a second implementation.") When
  you do split, the new boundary should make the system easier to read cold, not
  add a hop.

- **Single module.** One `go.mod` at the repo root; everything is a package
  beneath it. Encapsulation is a **local** property: nest an `internal/` exactly
  where hiding is needed (e.g. `clients/sextant-tui/internal/tui/`, an app's
  `internal/`), so the package is unimportable from outside that subtree and you
  can refactor its API freely.
- **Name packages for what they provide; ban `util`/`common`/`shared`/`helpers`/`base`/`lib`** —
  they become god-packages and breed cycles.
- **Dependencies point one direction**, and the strata edges are mechanically
  enforced: `importcheck` ([internal/importcheck](../../../internal/importcheck/importcheck.go))
  asserts the production dependency closures — the bus never imports a client; a
  convention reaches the SDK and the protocol bindings but never the bus; the TUI
  presentation strata reach NATS only through the SDK. When you add a package on
  one of these strata, add its `imports_test.go` assertion too.
- **A package is about one thing** — a cohesive API, not a junk drawer.
- **Flags as the self-documenting config surface** — layered with `SEXTANT_*` env defaults
  and saved client contexts per ADR-0021; no config frameworks.
- **No mutable package-level globals.** *(Also a judgment rule: an immutable
  lookup table is a legitimate, idiomatic global, and there is no clean
  mechanical exclusion that keeps those while banning mutable state — so the gate
  leaves this here rather than incur `//nolint` debt.)* Prefer a `const`, a pure
  function, or a value passed through a constructor. An **immutable** lookup
  table or preset (a palette, a fixed ordering, a sentinel `var ErrX =
  errors.New(...)`) is fine — it carries no shared mutable state. A mutable
  package global is not: thread it through a struct instead.

## Errors

- **Errors are values; handle each error exactly once** — no log-and-return (that
  double-reports).
- **Wrapping policy:** use stdlib `fmt.Errorf("...: %w", err)`. **Wrap errors that cross a
  module boundary** with `%w` plus the context the caller needs to make sense of the
  failure; **a bare return is fine within a module**, where the immediate caller already
  has the context. Match with `errors.Is`/`errors.As`. Use `%v` (not `%w`) when you
  deliberately do *not* want to expose the wrapped error as part of your API contract.
  *(errorlint enforces the `%w`/`Is`/`As` mechanics; this is the policy choice it can't make.)*
- **Assert on behavior, not concrete error type, where practical.**
- **Eliminate error handling by eliminating errors** where you can (Scanner-style memoized
  error so the loop body has none).
- **Handle errors and special cases early and return** — reduce nesting; keep the happy
  path left-aligned.
- **Never panic across package boundaries or in library code;** `os.Exit`/`log.Fatal` only
  in an app's `main`.

## Concurrency & Safety

- **Own every goroutine's lifecycle** — each has a clear exit path.
- **Make goroutine exit conditions explicit** — avoid leaks from blocked sends/receives.
- **`context.Context` is the first parameter, never nil, and is not stored in a
  struct** — with one sanctioned exception. *(This is a judgment rule: `containedctx`
  flags every ctx-in-struct, but a long-lived process, server, or Bubble Tea model
  legitimately holds the **lifetime** context it was built on — the bus server, the
  dispatch/workflow coordinators, the MCP `connManager`'s server-lifetime connect
  context, a TUI model. There is no clean mechanical line between those and an
  accidentally-captured request context, so the gate leaves the call here rather
  than carry path exclusions and `//nolint` debt.)* Pass ctx as the first
  argument to every function that needs one. Store a ctx in a struct **only** when
  the struct's whole life is bound to that context (a daemon/coordinator/program
  built on a signal or server-lifetime context); document *why* on the field.
  Never stash a per-request context in a struct that outlives the request — that
  is the leak the rule exists to catch.
- **Timeout anything crossing a process/network boundary;** use ctx-aware stdlib calls.
- **Producers close channels, not consumers;** buffered (cap 1) for fire-after-cancel.
- **Bound concurrency** (worker pools/semaphores); no unbounded `go` per request — the
  Wire API's worker slots are the house pattern.
- **Pick value or pointer semantics per type and stay consistent;** don't mix receiver types.
- **Beware struct-copy aliasing** — copying a struct that holds a slice/`Buffer` aliases the
  backing array.
- **Typed atomics (`atomic.Bool`/`Int64`) over raw int flags.**

## Naming

- **Short names scoped to distance from use** — `i`/`r`/`err` in tight scopes; descriptive
  across distance. Exported identifiers are maximum distance: spelled-out names, never
  abbreviations.
- **Consistent short receiver names; never `this`/`self`.**
- **Avoid stutter** — `bytes.Buffer`, not `bytes.BytesBuffer`.
- **Name for role, not representation** — no type encoded in the name.
- **Domain vocabulary is CONTEXT.md's** — when a name carries protocol meaning (message,
  artifact, client, lexicon, enroll/retire), use the shared language, not a synonym.

## Comments & Docs

- **Comment the why, not the what** — intent, rationale, non-obvious decisions, not a
  restatement of the code.
- **Document the contract, not the signature** — preconditions, invariants, what returned
  errors mean, ownership of returned values, concurrency-safety.
- **No commented-out code** — delete it; git has the history.
- **Explain "why-not" decisions and gotchas inline** where a future reader would otherwise
  re-litigate them. Decisions of ADR weight go in `docs/adr/` and get a pointer from the
  code, not a re-explanation.

## Testing internals

A ladder — use the lowest rung that works. The build tag is the escape hatch, not the
default; naming the lower rungs is what keeps it rare.

- **Same-package test** (`package foo` in `foo_test.go`) sees unexported identifiers
  directly. This *is* "testing an internal method" — the 95% case, no machinery.
- **Black-box test that needs one internal:** add an `export_test.go` (a `package foo`
  file, so it's test-only with zero production footprint) that re-exports just that symbol,
  letting an external `foo_test` package stay black-box while reaching the one thing it
  needs. Standard-library idiom (`net/http`, `time`).
- **A test in package A needs package B's internals:** first try writing it in B's
  *external* test package (`b_test`, in B's own directory) — it may import packages that
  import B (e.g. import A to drive the test), which is not a cycle. Combined with B's
  `export_test.go` this usually needs no build tag.
- **Last resort — genuinely can't relocate:** a build tag on the files that expose
  internals, so they vanish from production. It's the only mechanism that makes code truly
  absent from the binary yet reachable cross-package (`testing.Testing()` and env guards
  still ship the code; reflect/unsafe is brittle). Reserve it for this rung.
- **Bare `go test ./...` must always compile and stay green.** Don't gate via compile
  failure — a compile error reads as "package broken," not "test red," and breaks
  editors/gopls/vet. Sextant's one existing tag, `e2e`, is an opt-in *suite*, not an
  internals hatch; the tag-carrying discipline that keeps tagged files from rotting lives
  in the static-checks doc and applies to any new tag.
