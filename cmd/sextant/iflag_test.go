package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingLauncher implements tuiLauncher and captures the arguments
// it was invoked with so smoke tests can assert that the `-i` flag
// actually routed through the TUI launcher.
type recordingLauncher struct {
	called     bool
	configDir  string
	selectedID string
	returnErr  error
}

func (r *recordingLauncher) RunAgentsList(_ context.Context, configDir, selectedID string) error {
	r.called = true
	r.configDir = configDir
	r.selectedID = selectedID
	return r.returnErr
}

// withTUILauncher swaps activeTUILauncher for the duration of the test
// and restores the previous value via t.Cleanup.
func withTUILauncher(t *testing.T, l tuiLauncher) {
	t.Helper()
	prev := activeTUILauncher
	activeTUILauncher = l
	t.Cleanup(func() { activeTUILauncher = prev })
}

// TestAgentsListIFlagAcceptedAndRoutesToTUI verifies that `sextant
// agents list -i` parses the flag and dispatches through the TUI
// launcher seam. The seam captures the call without booting bubbletea.
func TestAgentsListIFlagAcceptedAndRoutesToTUI(t *testing.T) {
	rec := &recordingLauncher{returnErr: errors.New("ok")}
	withTUILauncher(t, rec)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"agents", "list", "-i"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "ok") {
		// We expect the launcher's stub error to propagate, proving the
		// TUI path was taken (the default RunE would have failed with a
		// connection error instead).
		t.Fatalf("Execute returned %v, want stub error 'ok'", err)
	}
	if !rec.called {
		t.Fatal("TUI launcher was not invoked")
	}
	if rec.selectedID != "" {
		t.Errorf("selectedID = %q, want empty (list -i passes no id)", rec.selectedID)
	}
}

// TestAgentsListIFlagLongFormTui verifies the --tui alias.
func TestAgentsListIFlagLongFormTui(t *testing.T) {
	rec := &recordingLauncher{returnErr: errors.New("ok")}
	withTUILauncher(t, rec)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"agents", "list", "--tui"})
	_ = root.Execute()
	if !rec.called {
		t.Fatal("TUI launcher was not invoked for --tui form")
	}
}

// TestAgentsListWithoutIFlagSkipsTUI verifies that the static path is
// taken when `-i` is absent. The static path attempts a real daemon
// connection — we accept any error here so long as the TUI seam is
// NOT touched.
func TestAgentsListWithoutIFlagSkipsTUI(t *testing.T) {
	rec := &recordingLauncher{}
	withTUILauncher(t, rec)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	// Point config-dir at a tmp dir so connectAgent fails fast on a
	// missing sextantd.toml rather than spinning on a real connection.
	root.SetArgs([]string{"--config-dir", t.TempDir(), "agents", "list"})
	_ = root.Execute()
	if rec.called {
		t.Fatal("TUI launcher was invoked without -i flag")
	}
}

// TestPendingListIFlagSurfacesFollowUp verifies the placeholder error
// message points the operator at the follow-up ticket.
func TestPendingListIFlagSurfacesFollowUp(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"pending", "list", "-i"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from pending list -i; got nil")
	}
	if !strings.Contains(err.Error(), "feat-tui-pending-component") {
		t.Errorf("error %q should mention the follow-up ticket", err.Error())
	}
}

// TestTracesShowIFlagSurfacesFollowUp is the traces equivalent.
func TestTracesShowIFlagSurfacesFollowUp(t *testing.T) {
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"traces", "show", "abc123", "-i"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error from traces show -i; got nil")
	}
	if !strings.Contains(err.Error(), "feat-tui-traces-component") {
		t.Errorf("error %q should mention the follow-up ticket", err.Error())
	}
}

// TestSanitizeOperatorName exercises the helper used by the iflag
// path to ensure it matches the standalone binary's behavior.
func TestSanitizeOperatorName(t *testing.T) {
	cases := map[string]string{
		"lena":          "lena",
		"lena.dev":      "lena_dev",
		"User Name":     "User_Name",
		"alice@example": "alice_example",
		"":              "",
	}
	for in, want := range cases {
		if got := sanitizeOperatorName(in); got != want {
			t.Errorf("sanitizeOperatorName(%q) = %q, want %q", in, got, want)
		}
	}
}
