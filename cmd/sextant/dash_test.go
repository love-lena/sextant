package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/tui/component"
)

// TestDashDumpDefaultConfig verifies that --dump-default-config
// prints the embedded TOML byte-for-byte to stdout without opening
// the TUI. Operators pipe this into ~/.config/sextant/config.toml
// to seed a customization, so any drift between the embedded blob
// and what `--dump-default-config` emits would break that flow.
func TestDashDumpDefaultConfig(t *testing.T) {
	t.Parallel()
	cmd := newDashCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--dump-default-config"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if got != defaultDashConfigTOML {
		t.Errorf("stdout mismatch with embedded default.\nGot:\n%s\nWant:\n%s",
			got, defaultDashConfigTOML)
	}
	// Sanity-check the marker the embedded TOML carries so a stray
	// CRLF / BOM doesn't silently slip past the byte-equality above.
	if !strings.Contains(got, `id = "agents"`) {
		t.Errorf("dumped default missing expected agents pane")
	}
	if !strings.Contains(got, `id = "conversation"`) {
		t.Errorf("dumped default missing expected conversation pane")
	}
	if !strings.Contains(got, `id = "pending"`) {
		t.Errorf("dumped default missing expected pending pane")
	}
}

// TestDashBuildPaneResolvesRegisteredComponent verifies the
// command-string → registered Component mapping. The conversation
// alias should resolve to the chat component (Meta.Command =
// "agents chat") even though the TOML uses the legacy verb.
func TestDashBuildPaneResolvesRegisteredComponent(t *testing.T) {
	t.Parallel()
	// Pull live registry (init() in agents + chat populates it).
	// We don't snapshot/restore because Register panics on dup, so
	// the test must not mutate it.
	metas := liveRegistryForDashTests(t)

	cases := []struct {
		name        string
		pc          paneConfig
		wantHosted  bool
		wantCommand string
	}{
		{
			name:        "agents list resolves",
			pc:          paneConfig{ID: "agents", Command: "agents list"},
			wantHosted:  true,
			wantCommand: "agents list",
		},
		{
			name:        "conversation alias resolves to chat",
			pc:          paneConfig{ID: "conversation", Command: "conversation $selected_agent"},
			wantHosted:  true,
			wantCommand: "conversation $selected_agent",
		},
		{
			name:        "pending list resolves to registered component",
			pc:          paneConfig{ID: "pending", Command: "pending list"},
			wantHosted:  true,
			wantCommand: "pending list",
		},
		{
			name:        "unregistered command falls back to placeholder",
			pc:          paneConfig{ID: "files", Command: "files ls"},
			wantHosted:  false,
			wantCommand: "files ls",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := buildPane(tc.pc, metas, "tester", "")
			if p == nil {
				t.Fatal("buildPane returned nil")
			}
			if tc.wantHosted && p.host == nil {
				t.Errorf("expected hosted Component, got placeholder %q", p.placeholder)
			}
			if !tc.wantHosted && p.host != nil {
				t.Errorf("expected placeholder, got hosted Component")
			}
			if p.command != tc.wantCommand {
				t.Errorf("command = %q, want %q", p.command, tc.wantCommand)
			}
		})
	}
}

// TestDashSplitCommand verifies the heuristic that separates the
// command path (matched against Meta.Command) from template
// arguments. Templates always start with `$`; everything before the
// first `$`-prefixed token is the command path.
func TestDashSplitCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantCmd  string
		wantArgs []string
	}{
		{in: "agents list", wantCmd: "agents list", wantArgs: nil},
		{in: "conversation $selected_agent", wantCmd: "conversation", wantArgs: []string{"$selected_agent"}},
		{in: "pending list", wantCmd: "pending list", wantArgs: nil},
		{in: "", wantCmd: "", wantArgs: nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotArgs := splitCommand(tc.in)
			if gotCmd != tc.wantCmd {
				t.Errorf("cmd = %q, want %q", gotCmd, tc.wantCmd)
			}
			if !equalStringSlice(gotArgs, tc.wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, tc.wantArgs)
			}
		})
	}
}

// liveRegistryForDashTests pulls the live component registry. The
// init() functions in pkg/tui/agents and pkg/tui/chat populate it on
// import — and dash.go's blank-imports drag those packages in.
func liveRegistryForDashTests(t *testing.T) []component.Meta {
	t.Helper()
	metas := component.List()
	if len(metas) == 0 {
		t.Fatal("component registry empty — agents + chat init() should have registered")
	}
	return metas
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
