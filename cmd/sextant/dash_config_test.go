package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadDashConfigDefaultEmbedded verifies the no-override path:
// when nothing exists at the override location, the loader returns
// the embedded default. The default ships with three panes (agents,
// conversation, pending) — this test pins that contract so accidental
// edits to the embedded TOML get caught by CI.
func TestLoadDashConfigDefaultEmbedded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// dir contains no config.toml; loader should fall back to the
	// embedded default.
	cfg, err := loadDashConfig(dir)
	if err != nil {
		t.Fatalf("loadDashConfig: %v", err)
	}
	if got, want := len(cfg.Dash.Panes), 3; got != want {
		t.Fatalf("len(panes) = %d, want %d", got, want)
	}
	wantIDs := []string{"agents", "conversation", "pending"}
	for i, w := range wantIDs {
		if cfg.Dash.Panes[i].ID != w {
			t.Errorf("panes[%d].ID = %q, want %q",
				i, cfg.Dash.Panes[i].ID, w)
		}
	}
}

// TestLoadDashConfigOverrideTakesPrecedence verifies that a
// config.toml in the override directory wins over the embedded
// default. We use a single-pane override so the default's count
// (3 panes) doesn't accidentally match.
func TestLoadDashConfigOverrideTakesPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "config.toml")
	override := `[[dash.panes]]
id = "only"
command = "agents list"
`
	if err := os.WriteFile(overridePath, []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	cfg, err := loadDashConfig(dir)
	if err != nil {
		t.Fatalf("loadDashConfig: %v", err)
	}
	if got, want := len(cfg.Dash.Panes), 1; got != want {
		t.Fatalf("len(panes) = %d, want %d (override should win)", got, want)
	}
	if cfg.Dash.Panes[0].ID != "only" {
		t.Errorf("panes[0].ID = %q, want %q", cfg.Dash.Panes[0].ID, "only")
	}
}

// TestLoadDashConfigMalformedTOML verifies that a syntactically
// broken override surfaces as a clean error rather than silently
// falling back to the default. Silent fallback would mask operator
// typos; the error surface is the load-bearing UX here.
func TestLoadDashConfigMalformedTOML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "config.toml")
	// Unterminated string is the simplest syntactic failure go-toml
	// surfaces with a useful error.
	if err := os.WriteFile(overridePath, []byte("[[dash.panes]\nid = \"broken"), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	_, err := loadDashConfig(dir)
	if err == nil {
		t.Fatal("expected error from malformed TOML, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse failure", err.Error())
	}
}

// TestLoadDashConfigDuplicateID verifies the validation rule: pane
// ids must be unique. Stickers cell ids and bubblezone marker keys
// both depend on this, so a duplicate would silently overwrite the
// first registration.
func TestLoadDashConfigDuplicateID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "config.toml")
	dup := `[[dash.panes]]
id = "agents"
command = "agents list"

[[dash.panes]]
id = "agents"
command = "agents list"
`
	if err := os.WriteFile(overridePath, []byte(dup), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	_, err := loadDashConfig(dir)
	if err == nil {
		t.Fatal("expected error from duplicate id, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q should mention duplicate id", err.Error())
	}
}

// TestLoadDashConfigEmptyPaneFields verifies the per-pane validation:
// a pane with an empty id or command is rejected. The dash needs
// both fields to resolve the pane to a Component.
func TestLoadDashConfigEmptyPaneFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "empty id",
			toml: `[[dash.panes]]
id = ""
command = "agents list"
`,
			want: "id is required",
		},
		{
			name: "empty command",
			toml: `[[dash.panes]]
id = "agents"
command = ""
`,
			want: "command is required",
		},
		{
			name: "no panes",
			toml: `[dash]
`,
			want: "at least one pane is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(dir, "config.toml"),
				[]byte(tc.toml), 0o644,
			); err != nil {
				t.Fatalf("write override: %v", err)
			}
			_, err := loadDashConfig(dir)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}
