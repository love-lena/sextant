package main

import "testing"

// TestDefaultStoreHonorsEnv pins the --store default precedence: $SEXTANT_STORE
// when set, a non-empty built-in path otherwise. An explicit --store overriding
// either is the flag package's job, not ours.
func TestDefaultStoreHonorsEnv(t *testing.T) {
	t.Setenv("SEXTANT_STORE", "/tmp/sextant-env-store")
	if got := defaultStore(); got != "/tmp/sextant-env-store" {
		t.Fatalf("defaultStore() = %q, want $SEXTANT_STORE value", got)
	}

	t.Setenv("SEXTANT_STORE", "")
	got := defaultStore()
	if got == "" {
		t.Fatal("defaultStore() with no env returned empty; want a built-in default path")
	}
	if got == "/tmp/sextant-env-store" {
		t.Fatalf("defaultStore() = %q, want the built-in fallback once env is cleared", got)
	}
}
