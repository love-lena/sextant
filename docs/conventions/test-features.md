# Test-only features

Code that exists only to support testing — seams, fault injection, access to
another package's internals — must never ship in a production binary, and should
add as little surface as possible. Go gives us a ladder of mechanisms; **use the
lowest rung that works.** Reach for a higher rung only when the one below it
genuinely can't serve the case.

## The ladder

1. **Same-package helper → a plain `_test.go` file.**
   Compiled only into that package's test binary, never shipped, no extra
   surface. This is the default and covers the large majority of tests.

2. **Cross-package fake or builder that needs no internals → a normal helper
   package (e.g. `internal/<x>test`), imported only from `_test.go` files.**
   Because nothing in a production build imports it, the linker's package-level
   dead-code elimination keeps it out of every shipped binary automatically. No
   build tag needed. Use this for shared fakes, fixtures, and golden-data
   builders.

3. **A test that needs *another package's unexported internals*, in-process →
   `export_test.go` + an external test package.**
   Two Go rules combine to make this clean with **no build tag and no production
   seam**:
   - A file named `export_test.go` with `package <pkg>` is compiled only into
     `<pkg>`'s test binary, yet (being `package <pkg>`) it can reach unexported
     identifiers and re-export them for tests.
   - An **external** test package (`package <pkg>_test`, in the same directory)
     may import packages that themselves import `<pkg>` — the import cycle that
     blocks an *internal* (`package <pkg>`) test does not apply.

   So a test that must (a) drive a higher-level package and (b) poke `<pkg>`'s
   internals lives in `<pkg>_test`, imports the higher-level package to drive it,
   and reaches `<pkg>`'s internals through `export_test.go`. Host the test in the
   package that **owns** the internals it manipulates — that placement is honest,
   and it's what unlocks the visibility.

   *Worked example:* the bus's SDK↔bus integration tests
   (`pkg/bus/sdk_integration_test.go`, `package bus_test`) need to set up state a
   client cannot create for itself — a different protocol epoch, a corrupt
   registry record, a raw frame that bypasses stamping — and then assert the
   SDK's fail-loud / quarantine behaviour. `pkg/bus/export_test.go` re-exports the
   privileged backend writes; the test imports `pkg/sextant` to drive the real
   SDK. No build tag, no method on the production `*Bus`.

4. **A test-only hook needed inside a *built binary*, for *out-of-process* e2e →
   a build tag (`//go:build testfeatures`).**
   This is the only case rungs 1–3 cannot serve: when a separately-built `sextant`
   process (spawned by an e2e harness) must expose a fault-injection or
   inspection hook that the test reaches over the wire, not via the test binary.
   Then, and only then, gate the hook on a build tag so it is absent from release
   builds. **There is no such case today**, so the `testfeatures` tag does not yet
   exist — introduce it (and wire `-tags testfeatures` into `make test` + CI, and
   `--build-tags testfeatures` into the linter) when a real rung-4 need appears.

## Why not a build tag for rung 3

A build tag for in-process internals-access works, but it's the wrong tool: every
test invocation (including a bare `go test ./...` and the editor/`gopls`/`go vet`
default build) must pass the tag, or the package fails to *compile* — which reads
as "package broken," not "a test is skipped," and poisons tooling for the whole
package. The `export_test.go` route keeps a plain `go test ./...` compiling and
green with no flag. So tags are reserved for rung 4, where the feature lives in a
binary the test binary can't reach into.
