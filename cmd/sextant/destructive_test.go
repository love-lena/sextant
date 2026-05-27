package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDestructiveConfirmRefusesWithoutYes — the safety property:
// without --yes the helper returns errDestructiveNoYes naming the
// action and the operator-visible flag.
func TestDestructiveConfirmRefusesWithoutYes(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	_, err := flags.confirm(cmd, "kill agent foo")
	if err == nil {
		t.Fatalf("expected refusal, got nil")
	}
	if !errors.Is(err, errDestructiveNoYes) {
		t.Errorf("error = %v, want wrapping errDestructiveNoYes", err)
	}
	for _, want := range []string{"kill agent foo", "--yes", "--dry-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestDestructiveConfirmProceedsWithYes — with --yes the helper
// returns (true, nil); the verb's RunE proceeds.
func TestDestructiveConfirmProceedsWithYes(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set --yes: %v", err)
	}
	proceed, err := flags.confirm(cmd, "stop daemon")
	if err != nil {
		t.Fatalf("confirm err = %v", err)
	}
	if !proceed {
		t.Errorf("proceed = false with --yes, want true")
	}
}

// TestDestructiveConfirmDryRunShortCircuits — --dry-run prints what
// the action would have done and returns (false, nil) so the verb's
// RunE exits cleanly without issuing the RPC.
func TestDestructiveConfirmDryRunShortCircuits(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	if err := cmd.Flags().Set("dry-run", "true"); err != nil {
		t.Fatalf("set --dry-run: %v", err)
	}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	proceed, err := flags.confirm(cmd, "archive every defined agent")
	if err != nil {
		t.Fatalf("confirm err = %v", err)
	}
	if proceed {
		t.Errorf("proceed = true with --dry-run, want false (short-circuit)")
	}
	got := stderr.String()
	for _, want := range []string{"[dry-run]", "archive every defined agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run stderr %q missing %q", got, want)
		}
	}
}
