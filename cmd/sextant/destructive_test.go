package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDestructiveConfirmRefusesWithoutYesNonTTY — the safety
// property on the scripted path: without --yes and without a TTY,
// the helper returns errDestructiveNoYes naming the action and the
// operator-visible flag.
func TestDestructiveConfirmRefusesWithoutYesNonTTY(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return false }
	_, err := flags.confirm(cmd, "stop agent foo")
	if err == nil {
		t.Fatalf("expected refusal, got nil")
	}
	if !errors.Is(err, errDestructiveNoYes) {
		t.Errorf("error = %v, want wrapping errDestructiveNoYes", err)
	}
	for _, want := range []string{"stop agent foo", "--yes", "--dry-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestDestructiveConfirmProceedsWithYes — with --yes the helper
// returns (true, nil); the verb's RunE proceeds. --yes wins over
// the TTY check (no prompt should render even on an interactive
// stdin).
func TestDestructiveConfirmProceedsWithYes(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return true }
	promptCalls := 0
	flags.prompt = func(_, _ string) (bool, error) {
		promptCalls++
		return false, nil
	}
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
	if promptCalls != 0 {
		t.Errorf("prompt called %d times with --yes, want 0", promptCalls)
	}
}

// TestDestructiveConfirmDryRunShortCircuits — --dry-run prints what
// the action would have done and returns (false, nil) so the verb's
// RunE exits cleanly without issuing the RPC. --dry-run wins over
// the TTY check.
func TestDestructiveConfirmDryRunShortCircuits(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return true }
	promptCalls := 0
	flags.prompt = func(_, _ string) (bool, error) {
		promptCalls++
		return false, nil
	}
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
	if promptCalls != 0 {
		t.Errorf("prompt called %d times with --dry-run, want 0", promptCalls)
	}
	got := stderr.String()
	for _, want := range []string{"[dry-run]", "archive every defined agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run stderr %q missing %q", got, want)
		}
	}
}

// TestDestructiveConfirmTTYPromptYes — on an interactive stdin
// with no --yes / --dry-run, the helper renders the Huh confirm
// and returns (true, nil) when the operator answers Yes. The
// `action` string is passed through as the prompt description so
// the operator sees the resource being targeted.
func TestDestructiveConfirmTTYPromptYes(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return true }
	var gotTitle, gotDesc string
	flags.prompt = func(title, description string) (bool, error) {
		gotTitle = title
		gotDesc = description
		return true, nil
	}
	proceed, err := flags.confirm(cmd, "stop agent foo (abc123)")
	if err != nil {
		t.Fatalf("confirm err = %v", err)
	}
	if !proceed {
		t.Errorf("proceed = false after Yes, want true")
	}
	if gotTitle == "" {
		t.Errorf("prompt called with empty title")
	}
	if gotDesc != "stop agent foo (abc123)" {
		t.Errorf("prompt description = %q, want exact action string", gotDesc)
	}
}

// TestDestructiveConfirmTTYPromptNo — on an interactive stdin
// with no --yes / --dry-run, when the operator answers No, the
// helper returns (false, nil) (clean exit 0) and writes an
// `aborted: <action>` line to stderr so the operator sees the
// command short-circuited intentionally.
func TestDestructiveConfirmTTYPromptNo(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return true }
	flags.prompt = func(_, _ string) (bool, error) {
		return false, nil
	}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	proceed, err := flags.confirm(cmd, "stop agent foo (abc123)")
	if err != nil {
		t.Fatalf("confirm err = %v, want nil (No is clean exit)", err)
	}
	if proceed {
		t.Errorf("proceed = true after No, want false")
	}
	got := stderr.String()
	for _, want := range []string{"aborted", "stop agent foo (abc123)"} {
		if !strings.Contains(got, want) {
			t.Errorf("aborted stderr %q missing %q", got, want)
		}
	}
}

// TestDestructiveConfirmTTYPromptError — if the Huh form itself
// errors (e.g. operator hits Ctrl+C), the helper wraps the error
// and returns (false, err) so the caller surfaces it.
func TestDestructiveConfirmTTYPromptError(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	flags := newDestructiveFlags(cmd)
	flags.isTTY = func() bool { return true }
	sentinel := errors.New("user cancelled")
	flags.prompt = func(_, _ string) (bool, error) {
		return false, sentinel
	}
	proceed, err := flags.confirm(cmd, "stop agent foo")
	if proceed {
		t.Errorf("proceed = true on prompt error, want false")
	}
	if err == nil {
		t.Fatalf("expected error from prompt, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want wrapping %v", err, sentinel)
	}
}
