package rpc

import "testing"

// TestCapForMatchesLegacySwitch pins CapFor's verb→capability mapping to
// the exact values the pre-VerbSpec hand-written switch returned. C2 is a
// pure refactor: the capability for every existing verb must be
// byte-identical, or an operator's capability check changes meaning.
func TestCapForMatchesLegacySwitch(t *testing.T) {
	// The legacy switch, transcribed verbatim from the pre-C2 types.go.
	legacy := func(verb string) string {
		switch verb {
		case VerbListAgents, VerbGetAgentStatus:
			return "read.agents"
		case VerbQueryHistory, VerbQueryAudit, VerbQueryTrace:
			return "read.history"
		case VerbReadFile, VerbListDir, VerbStat:
			return "read.container_files"
		case VerbExecInContainer:
			return "control.exec"
		case VerbSpawnAgent:
			return "control.spawn"
		case VerbKillAgent:
			return "control.kill"
		case VerbRestartAgent:
			return "control.restart"
		case VerbPromptAgent:
			return "control.prompt"
		case VerbArchiveAgent:
			return "control.archive"
		case VerbWorktreeCreate, VerbWorktreeDestroy, VerbWorktreeMerge:
			return "control.worktree"
		case VerbWorktreeList, VerbWorktreeDiff:
			return "read.worktrees"
		case VerbGetVersion:
			return ""
		default:
			return ""
		}
	}

	verbs := []string{
		VerbListAgents, VerbGetAgentStatus, VerbReadFile, VerbQueryHistory,
		VerbSpawnAgent, VerbKillAgent, VerbPromptAgent, VerbArchiveAgent,
		VerbRestartAgent, VerbListDir, VerbStat, VerbExecInContainer,
		VerbQueryAudit, VerbQueryTrace, VerbWorktreeCreate, VerbWorktreeDestroy,
		VerbWorktreeList, VerbWorktreeMerge, VerbWorktreeDiff, VerbGetVersion,
		// Unknown verb must still resolve to "" (deny-by-default lane).
		"definitely_not_a_verb",
	}
	for _, v := range verbs {
		if got, want := CapFor(v), legacy(v); got != want {
			t.Errorf("CapFor(%q) = %q, legacy switch = %q", v, got, want)
		}
	}
}

// TestVerbSpecsWellFormed asserts the table itself is internally
// consistent: unique non-empty names, a valid phase, and non-nil
// Req/Resp samples for every row (the schema generator dereferences
// these). A drift here is the exact class of bug the single table
// exists to prevent.
func TestVerbSpecsWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(VerbSpecs))
	for i, s := range VerbSpecs {
		if s.Name == "" {
			t.Errorf("VerbSpecs[%d]: empty Name", i)
		}
		if seen[s.Name] {
			t.Errorf("VerbSpecs[%d]: duplicate Name %q", i, s.Name)
		}
		seen[s.Name] = true
		if s.Phase != PhaseInitial && s.Phase != PhaseLifecycle && s.Phase != PhaseWorktree {
			t.Errorf("VerbSpecs[%d] (%q): invalid Phase %d", i, s.Name, s.Phase)
		}
		if s.Req == nil {
			t.Errorf("VerbSpecs[%d] (%q): nil Req sample", i, s.Name)
		}
		if s.Resp == nil {
			t.Errorf("VerbSpecs[%d] (%q): nil Resp sample", i, s.Name)
		}
		if got := CapFor(s.Name); got != s.Capability {
			t.Errorf("VerbSpecs[%d] (%q): CapFor=%q, spec.Capability=%q", i, s.Name, got, s.Capability)
		}
	}
}

// TestVerbSpecsCoverEveryConst asserts every Verb* constant has exactly
// one row in the table — no verb-name constant left without a spec.
func TestVerbSpecsCoverEveryConst(t *testing.T) {
	allConsts := []string{
		VerbListAgents, VerbGetAgentStatus, VerbReadFile, VerbQueryHistory,
		VerbSpawnAgent, VerbKillAgent, VerbPromptAgent, VerbArchiveAgent,
		VerbRestartAgent, VerbListDir, VerbStat, VerbExecInContainer,
		VerbQueryAudit, VerbQueryTrace, VerbWorktreeCreate, VerbWorktreeDestroy,
		VerbWorktreeList, VerbWorktreeMerge, VerbWorktreeDiff, VerbGetVersion,
	}
	inTable := make(map[string]bool, len(VerbSpecs))
	for _, s := range VerbSpecs {
		inTable[s.Name] = true
	}
	for _, c := range allConsts {
		if !inTable[c] {
			t.Errorf("verb const %q has no VerbSpec row", c)
		}
	}
	if len(VerbSpecs) != len(allConsts) {
		t.Errorf("VerbSpecs has %d rows, %d verb consts — table has an extra/missing row", len(VerbSpecs), len(allConsts))
	}
}
