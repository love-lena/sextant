package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/version"
)

// TestRunVersionFormatsVersionAndCommit pins the `sextantd version` output
// shape. Must match `sextant version` byte-for-byte modulo the values
// themselves so operators see a single format across binaries.
func TestRunVersionFormatsVersionAndCommit(t *testing.T) {
	prevV, prevC := version.Version, version.Commit
	version.Version = "v9.9.9-test"
	version.Commit = "deadbee"
	t.Cleanup(func() {
		version.Version = prevV
		version.Commit = prevC
	})

	var out bytes.Buffer
	if err := runVersion(&out); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "v9.9.9-test") {
		t.Errorf("output missing Version: %q", got)
	}
	if !strings.Contains(got, "deadbee") {
		t.Errorf("output missing Commit: %q", got)
	}
	if want := "v9.9.9-test (deadbee)\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
