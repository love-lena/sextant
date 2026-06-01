package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRootCmdWiring asserts that every documented command path resolves
// to a registered Cobra command. This is the wiring test the migration
// ticket calls for — one assertion per migrated verb so a future
// re-shuffle that drops a command from RootCmd fails loudly.
func TestRootCmdWiring(t *testing.T) {
	paths := [][]string{
		// Singletons.
		{"init"},
		{"doctor"},
		{"version"},

		// agents.*
		{"agents", "list"},
		{"agents", "show"},
		{"agents", "create"},
		{"agents", "stop"},
		{"agents", "restart"},
		{"agents", "archive"},
		{"agents", "prompt"},
		{"agents", "chat"},
		{"agents", "exec"},

		// pending.*
		{"pending", "list"},
		{"pending", "answer"},
		{"pending", "defer"},
		{"pending", "escalate"},

		// files.*
		{"files", "read"},
		{"files", "ls"},
		{"files", "tail"},

		// worktree.*
		{"worktree", "list"},
		{"worktree", "create"},
		{"worktree", "delete"},
		{"worktree", "merge"},
		{"worktree", "diff"},
		{"worktree", "prune"},

		// templates.*
		{"templates", "reload"},

		// audit.*
		{"audit", "list"},
		{"audit", "tail"},

		// traces.*
		{"traces", "show"},

		// daemon.* (NEW)
		{"daemon", "start"},
		{"daemon", "stop"},
		{"daemon", "restart"},
		{"daemon", "status"},
		{"daemon", "logs"},

		// events.* (NEW)
		{"events", "tail"},

		// theme.* (NEW — sextant theme list/import/show)
		{"theme", "list"},
		{"theme", "import"},
		{"theme", "show"},

		// Aliases (legacy top-level verbs, hidden but resolvable).
		{"ask"},
		{"conversation"},
		{"tail"},
		{"exec"},
		{"start"},
		{"stop"},
		{"restart"},
		{"status"},
		{"logs"},
	}

	root := newRootCmd()
	for _, path := range paths {
		t.Run(strings.Join(path, "."), func(t *testing.T) {
			c, _, err := root.Find(path)
			if err != nil {
				t.Fatalf("Find(%v): %v", path, err)
			}
			// Last token should match the resolved command's name.
			if c == nil {
				t.Fatalf("Find(%v) returned nil command", path)
			}
			if c.Name() != path[len(path)-1] {
				t.Errorf("Find(%v) resolved to %q, want %q", path, c.Name(), path[len(path)-1])
			}
		})
	}
}

// TestLegacyVerbAliasesResolve pins the four backwards-compat aliases
// introduced in the closed-exception verb migration
// (slug:feat-cli-verb-vocabulary-decision). Each old verb
// must continue to resolve to the same cobra command its new spelling
// resolves to — so `sextant agents kill foo` still runs the stop
// handler, etc. Aliases are scheduled for removal in v0.2; the test
// guards against accidentally dropping them sooner.
func TestLegacyVerbAliasesResolve(t *testing.T) {
	cases := []struct {
		alias  []string
		canon  []string
		wantUs string
	}{
		{alias: []string{"agents", "spawn"}, canon: []string{"agents", "create"}, wantUs: "create <name>"},
		{alias: []string{"agents", "kill"}, canon: []string{"agents", "stop"}, wantUs: "stop <agent>"},
		{alias: []string{"audit", "query"}, canon: []string{"audit", "list"}, wantUs: "list"},
		{alias: []string{"worktree", "destroy"}, canon: []string{"worktree", "delete"}, wantUs: "delete <name>"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.alias, "."), func(t *testing.T) {
			root := newRootCmd()
			aliasCmd, _, err := root.Find(tc.alias)
			if err != nil {
				t.Fatalf("alias %v does not resolve: %v", tc.alias, err)
			}
			canonCmd, _, err := root.Find(tc.canon)
			if err != nil {
				t.Fatalf("canonical %v does not resolve: %v", tc.canon, err)
			}
			// Cobra Aliases route both Find calls to the same *Command.
			if aliasCmd != canonCmd {
				t.Fatalf("alias %v resolved to %p (Use=%q); canonical %v resolved to %p (Use=%q) — must be the same command",
					tc.alias, aliasCmd, aliasCmd.Use, tc.canon, canonCmd, canonCmd.Use)
			}
			if aliasCmd.Use != tc.wantUs {
				t.Errorf("alias %v Use=%q, want %q", tc.alias, aliasCmd.Use, tc.wantUs)
			}
		})
	}
}

// TestRemedyVerbsResolveInCobraTree pins Codex's defense-in-depth ask:
// every remedy command the CLI emits in structured errors and check
// verdicts must resolve to a real command in the cobra tree. Catches
// the class of bug Codex flagged where `agents resume` was suggested
// as the paused-agent remedy but never registered as a verb.
//
// New remedies added in handler / verdict / timeout error paths must
// add their verb path here. Run `rg "sextant agents|sextant doctor|sextant daemon" pkg cmd`
// after adding a new remedy to confirm coverage.
func TestRemedyVerbsResolveInCobraTree(t *testing.T) {
	remedyPaths := [][]string{
		{"agents", "list"},
		{"agents", "restart"},
		{"agents", "check"},
		{"doctor"},
		{"daemon", "start"},
	}
	root := newRootCmd()
	for _, path := range remedyPaths {
		t.Run(strings.Join(path, "."), func(t *testing.T) {
			c, _, err := root.Find(path)
			if err != nil {
				t.Fatalf("remedy verb %v does not resolve: %v", path, err)
			}
			if c == nil || c.Name() != path[len(path)-1] {
				t.Fatalf("remedy verb %v resolved to wrong command (got %v)", path, c)
			}
		})
	}
}

// TestRootHelpRuns verifies `sextant --help` exits cleanly (no error
// banner, no missing command). Smoke test for the Fang integration.
func TestRootHelpRuns(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"--help"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute --help: %v", err)
	}
}

// TestVersionCmdPrintsVersion ensures the singleton `version` command
// writes a non-empty line. Format-specific assertions live in version_test.go.
func TestVersionCmdPrintsVersion(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"version"})
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute version: %v", err)
	}
	if stdout.Len() == 0 {
		t.Errorf("version output was empty")
	}
}
