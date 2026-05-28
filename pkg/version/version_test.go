package version

import "testing"

func TestStringMatchesVersion(t *testing.T) {
	if got, want := String(), Version; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestVersionIsNonEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

// TestCommitDefaultIsUnknown documents the sentinel returned when the
// binary is built without -ldflags (`go run`, `go test`). Tooling that
// surfaces the commit relies on this value being non-empty.
func TestCommitDefaultIsUnknown(t *testing.T) {
	// The test binary is itself built without our Makefile -ldflags, so
	// Commit should retain its source default. If a future build pipeline
	// injects Commit for tests, drop this assertion.
	if Commit == "" {
		t.Fatal("Commit must not be empty (expected fallback sentinel)")
	}
}
