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
	called             bool
	pendingCalled      bool
	tracesID           string
	contextProjectsDir string
	contextSessionID   string
	daemonLogPath      string
	configDir          string
	selectedID         string
	returnErr          error
}

func (r *recordingLauncher) RunAgentsList(_ context.Context, configDir, selectedID string) error {
	r.called = true
	r.configDir = configDir
	r.selectedID = selectedID
	return r.returnErr
}

func (r *recordingLauncher) RunPendingList(_ context.Context, configDir string) error {
	r.pendingCalled = true
	r.configDir = configDir
	return r.returnErr
}

func (r *recordingLauncher) RunTracesShow(_ context.Context, configDir, traceID string) error {
	r.tracesID = traceID
	r.configDir = configDir
	return r.returnErr
}

func (r *recordingLauncher) RunAgentsContext(_ context.Context, configDir, projectsDir, sessionID string) error {
	r.contextProjectsDir = projectsDir
	r.contextSessionID = sessionID
	r.configDir = configDir
	return r.returnErr
}

func (r *recordingLauncher) RunDaemonLogs(_ context.Context, logPath string) error {
	r.daemonLogPath = logPath
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

// TestPendingListIFlagRoutesToTUI verifies `pending list -i` dispatches
// through the TUI launcher seam (replaces the old placeholder test now
// that pkg/tui/pending exists).
func TestPendingListIFlagRoutesToTUI(t *testing.T) {
	rec := &recordingLauncher{returnErr: errors.New("ok")}
	withTUILauncher(t, rec)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"pending", "list", "-i"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "ok") {
		t.Fatalf("Execute returned %v, want stub error 'ok'", err)
	}
	if !rec.pendingCalled {
		t.Fatal("pending TUI launcher not invoked under -i")
	}
}

// TestDaemonLogsIFlagRegistered confirms the `-i` / `--tui` flag is
// wired on `daemon logs`. (Full routing resolves the daemon config
// before the launcher, so launcher coverage lives in the
// pkg/tui/logsview tests.)
func TestDaemonLogsIFlagRegistered(t *testing.T) {
	cmd := newDaemonLogsCmd()
	if cmd.Flags().Lookup("tui") == nil {
		t.Fatal("daemon logs missing --tui flag")
	}
	if cmd.Flags().ShorthandLookup("i") == nil {
		t.Fatal("daemon logs missing -i shorthand")
	}
}

// TestAgentsContextIFlagRegistered confirms the `-i` / `--tui` flag is
// wired on `agents context`. (The full routing test would need a daemon
// stub — the command resolves the session-log paths before reaching the
// launcher — so coverage of the launcher itself lives in the
// pkg/tui/contextview tests.)
func TestAgentsContextIFlagRegistered(t *testing.T) {
	cmd := newAgentsContextCmd()
	if cmd.Flags().Lookup("tui") == nil {
		t.Fatal("agents context missing --tui flag")
	}
	if cmd.Flags().ShorthandLookup("i") == nil {
		t.Fatal("agents context missing -i shorthand")
	}
}

// TestTracesShowIFlagRoutesToTUI verifies `traces show <id> -i`
// dispatches through the launcher seam with the trace id (replaces the
// old placeholder test now that pkg/tui/traces exists).
func TestTracesShowIFlagRoutesToTUI(t *testing.T) {
	rec := &recordingLauncher{returnErr: errors.New("ok")}
	withTUILauncher(t, rec)

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"traces", "show", "abc-123", "-i"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "ok") {
		t.Fatalf("Execute returned %v, want stub error 'ok'", err)
	}
	if rec.tracesID != "abc-123" {
		t.Fatalf("traces launcher got id %q, want abc-123", rec.tracesID)
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
