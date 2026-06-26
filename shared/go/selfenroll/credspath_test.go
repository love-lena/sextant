package selfenroll_test

import (
	"testing"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/shared/go/selfenroll"
)

// TestEnrollCredsPathMatchesBus pins the read side to the write side:
// selfenroll computes the enrollment credential's location by store convention
// because it deliberately avoids linking the embedded bus server into client
// binaries, and pkg/bus.EnrollCredsPath is where the bus actually provisions
// the file. Nothing in production code ties the two, so a drift would break
// first-run enrollment silently — this test is the tie. It lives in an
// external test package (selfenroll_test) so the dependency direction stays
// honest: pkg/bus enters the test binary only, never the package.
func TestEnrollCredsPathMatchesBus(t *testing.T) {
	store := t.TempDir()
	got := selfenroll.EnrollCredsPath(store)
	want := bus.EnrollCredsPath(store)
	if got != want {
		t.Fatalf("selfenroll resolves the enrollment credential at %q, but the bus provisions it at %q — first-run enrollment would never find the file", got, want)
	}
}
