---
name: go-house-style
description: Authoritative house style and design conventions for writing Go in sextant. Use whenever writing, reviewing, refactoring, or designing Go code, APIs, or packages in this repo — it covers API and interface design, package layout, error-handling policy, concurrency and goroutine discipline, naming, comments, and core engineering philosophy. Consult it before writing any non-trivial Go, even when the task doesn't explicitly mention "style." Mechanical, lint-enforced rules live in the static-checks gate (make check), not here; this skill is the judgment layer for decisions a linter can't make.
---

# Go House Style — the judgment layer

This skill carries the opinions a tool can't enforce. Anything mechanically checkable
(formatting, naked returns, no `init()`/globals, error-checking, doc-comment presence,
Printf wrappers) is enforced by the **static-checks gate** — `make check`, designed in
[docs/agents/go-static-checks.md](../../../docs/agents/go-static-checks.md) — and is
**not restated here**: don't spend judgment re-deriving what the gate already decides.

Baseline: **single Go module**, version pinned in `go.mod` (currently `go 1.26`).

## Philosophy

- **Priority order: clarity > simplicity > concision > maintainability > consistency.**
  When two readings of "better" conflict, the higher priority wins — judged through the
  reader's eyes, not the author's.
- **Optimize for the reader, not the author.**
- **Anti-pattern — gratuitous inconsistency:** don't introduce a novel way to do something
  the codebase already does a standard way.
- **Scout's rule:** improve code you pass while working. Refactoring is fine — the risk
  isn't size, it's a change that's actually *worse* (a regression in clarity or correctness
  dressed up as a refactor). The test is the priority order: when you can't reasonably argue
  the existing code is better, fix it up; when the existing code might be better, leave it.
- **A little copying is better than a little dependency.**
- **Least mechanism:** reach for the simplest construct that works; stdlib over a framework.
  (This is the style-layer face of the repo's bright lines — primitives not policy, thin
  universal core.)

## APIs & Interfaces

> Guiding principle: **the easy way to use an API should be the correct way.** If the
> obvious call is also the safe call, most of the rest follows.

- **Escalate new or changed interfaces to Lena.** A new or modified interface — especially
  an exported one in `pkg/` — is among the hardest things to reverse once it has callers,
  so it's the line where autonomy stops. An agent can make most decisions on its own, but
  here it must deliberately consider the design, weigh the alternatives and choose the best
  one, and then ask for review *before* committing. In this repo that's the existing loop —
  committed ⇔ human-signed-off — applied early: flag the interface in the PR description
  (or the ticket) rather than letting it ride in silently. A new or changed interface is
  never shipped on autonomous judgment alone.
- **Accept interfaces, return concrete structs.** Producers return the concrete type so
  callers get the full API and you can add methods without breaking them. Exception: return
  an interface when genuinely yielding multiple types, or to hide impl in a library.
- **Consumer-defined, small interfaces.** Define the interface where it's *consumed*, not
  beside the implementation; keep it small. Reject the 20-method `Storer` next to its impl.
  (`internal/backend.Backend` is the house example: one interface, defined where the bus
  consumes it, with a conformance suite any implementation can run.)
- **Compile-time interface checks (`var _ Iface = (*T)(nil)`)** — use sparingly, only where
  nothing else exercises the satisfaction.
- **Make the zero value useful** — usable with no constructor where you can manage it.
- **One obvious constructor; functional options for optional config** — so adding a knob
  isn't a breaking signature change.
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

*(Comma-ok type assertions are required but gate-enforced.)*

## Packages, Layout & Modules

- **Single module.** Libs are packages under the root `go.mod`; use **`internal/`** to make
  packages unimportable and refactor their APIs freely.
- **`cmd/` for multiple binaries.**
- **Name packages for what they provide; ban `util`/`common`/`shared`/`helpers`/`base`/`lib`** —
  they become god-packages and breed cycles.
- **No `pkg/`; reject `golang-standards/project-layout`** as non-canonical. *(The current
  `pkg/` tree predates this rule — migrating it, and deciding which packages are the public
  SDK surface, is [[feat-layout-no-pkg]]. Don't add new packages under `pkg/`.)*
- **Dependencies point one direction** — domain packages don't import infra. *(The concrete
  layer ruleset is settled by [[feat-layout-no-pkg]]; depguard then enforces it
  mechanically.)*
- **A package is about one thing** — a cohesive API, not a junk drawer.
- **Flags as the self-documenting config surface.**

## Deep Modules

- **Prefer deep modules:** a small exported surface hiding substantial complexity
  (`net/http`). A good package hides more than it shows.
- **Review question:** *how much does this package's API hide?* Lots of surface exposing
  little is a shallow module — suspect it.
- **Avoid shallow / pass-through packages** that add a layer without adding abstraction;
  prefer fewer, deeper packages. An abstraction should pay for its complexity. (Same
  instinct as the repo's "abstract only against a second implementation" bright line.)

## Errors

- **Errors are values; handle each error exactly once** — no log-and-return (that
  double-reports).
- **Wrapping policy — LOCKED DEFAULT (override only by team decision):**
  use stdlib `fmt.Errorf("...: %w", err)`; **libraries (`pkg/`, `internal/`) return root
  errors**, **applications (`cmd/`) add wrapping context**; match with
  `errors.Is`/`errors.As`/`errors.AsType` (1.26). Use `%v` (not `%w`) when you deliberately
  do *not* want to expose the wrapped error as part of your API contract. *(errorlint
  enforces the mechanics; this is the policy.)*
- **Assert on behavior, not concrete error type, where practical.**
- **Eliminate error handling by eliminating errors** where you can (Scanner-style memoized
  error so the loop body has none).

## Concurrency & Safety

- **Own every goroutine's lifecycle** — each has a clear exit path.
- **Make goroutine exit conditions explicit** — avoid leaks from blocked sends/receives.
- **`context.Context` is the first parameter, never nil.** *(Never-stored-in-a-struct is
  gate-enforced; the bus's relay registry shows the registry-of-cancels alternative.)*
- **Timeout anything crossing a process/network boundary;** use ctx-aware stdlib calls.
- **Producers close channels, not consumers;** buffered (cap 1) for fire-after-cancel.
- **Bound concurrency** (worker pools/semaphores); no unbounded `go` per request — the
  Wire API's `apiSem` worker slots are the house pattern.
- **Pick value or pointer semantics per type and stay consistent;** don't mix receiver types.
- **Beware struct-copy aliasing** — copying a struct that holds a slice/`Buffer` aliases the
  backing array.
- **Typed atomics (`atomic.Bool`/`Int64`) over raw int flags.**

## Construction & State

- **Dependency injection via explicit constructor params; no DI frameworks.**

*(No `init()` and no package-level global state are gate-enforced.)*

## Panics

- **Never panic across package boundaries or in library code;** `os.Exit`/`log.Fatal` only
  in `main`.

## Naming

- **Short names scoped to distance from use** — `i`/`r`/`err` in tight scopes; descriptive
  across distance.
- **Consistent short receiver names; never `this`/`self`.**
- **Avoid stutter** — `bytes.Buffer`, not `bytes.BytesBuffer`.
- **Name for role, not representation** — no type encoded in the name.
- **No short/abbreviated names in exported API** — exported identifiers (package APIs,
  exported types/functions/methods/fields) get descriptive, spelled-out names. Short names
  are confined to small local scopes.
- **Domain vocabulary is CONTEXT.md's** — when a name carries protocol meaning (message,
  artifact, client, lexicon, enroll/retire), use the shared language, not a synonym.

## Control Flow

- **Reduce nesting; handle errors/special cases early and return** — keep the happy path
  left-aligned.

*(Naked returns are banned and gate-enforced.)*

## Comments & Docs

- **Comment the why, not the what** — intent, rationale, non-obvious decisions, not a
  restatement of the code.
- **Doc comments on exported identifiers** as complete sentences starting with the
  identifier's name. *(Presence is gate-enforced; quality is on you.)*
- **Package doc comment** — `// Package x ...` in one file, or `doc.go` for larger packages.
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
  editors/gopls/vet. Sextant already carries one tag — `e2e`, an opt-in *suite* (not an
  internals-exposure hatch) — and the gate keeps it honest: `make e2e` and CI run it, and
  golangci-lint lints with `build-tags: [e2e]` so tagged files can't rot. Any future tag
  must carry the same three (test run, lint run, editor config).
