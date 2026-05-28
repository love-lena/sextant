package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/version"
)

// TestVersionCmdFormatsVersionAndCommit pins the public stdout format
// (`<Version> (<Commit>)`). Operators script against it; treat changes as
// a documented break.
func TestVersionCmdFormatsVersionAndCommit(t *testing.T) {
	prevV, prevC := version.Version, version.Commit
	version.Version = "v9.9.9-test"
	version.Commit = "deadbee"
	t.Cleanup(func() {
		version.Version = prevV
		version.Commit = prevC
	})

	cmd := newVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
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
