package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProvisionInfraCredsAreOwnerOnly: the operator/enrollment credentials
// authorize identity issuance and retirement, so provisioning must leave them
// owner-only (0600) even when a reused store already holds a looser leftover —
// os.WriteFile would otherwise truncate the old file without tightening its mode.
func TestProvisionInfraCredsAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	// Simulate a reused store with a world-readable leftover operator credential.
	if err := os.WriteFile(filepath.Join(dir, "operator.creds"), []byte("stale"), 0o666); err != nil {
		t.Fatal(err)
	}
	b, err := Start(t.Context(), Config{StoreDir: dir})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Shutdown()

	for _, p := range []string{OperatorCredsPath(dir), EnrollCredsPath(dir)} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 600", p, perm)
		}
	}
}
