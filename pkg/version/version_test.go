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
