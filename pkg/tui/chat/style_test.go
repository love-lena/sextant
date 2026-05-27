package chat

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestStyleRoleTokensDefined asserts every named role this package
// uses is reachable from defaultStyles() and renders to a non-empty
// ANSI string. The point is not visual fidelity — it's that callers
// never reach past the role lookup into raw hex.
func TestStyleRoleTokensDefined(t *testing.T) {
	t.Parallel()
	s := defaultStyles()
	roles := map[string]func() string{
		"activeBorder":        func() string { return s.ActiveBorder.Render("x") },
		"attention":           func() string { return s.Attention.Render("x") },
		"destructive":         func() string { return s.Destructive.Render("x") },
		"success":             func() string { return s.Success.Render("x") },
		"streamPane":          func() string { return s.StreamPane.Render("x") },
		"composerPane":        func() string { return s.ComposerPane.Render("x") },
		"selectedRow":         func() string { return s.SelectedRow.Render("x") },
		"nonSelectedRow":      func() string { return s.NonSelectedRow.Render("x") },
		"statusBar":           func() string { return s.StatusBar.Render("x") },
		"keyHintKey":          func() string { return s.KeyHintKey.Render("x") },
		"keyHintDesc":         func() string { return s.KeyHintDesc.Render("x") },
		"muted":               func() string { return s.Muted.Render("x") },
		"headerName":          func() string { return s.HeaderName.Render("x") },
		"headerBranch":        func() string { return s.HeaderBranch.Render("x") },
		"actorUser":           func() string { return s.ActorUser.Render("x") },
		"actorAgent":          func() string { return s.ActorAgent.Render("x") },
		"toolLine":            func() string { return s.ToolLine.Render("x") },
		"headerRule":          func() string { return s.HeaderRule.Render("x") },
		"turnDivider":         func() string { return s.TurnDivider.Render("x") },
		"statusNormal":        func() string { return s.StatusNormal.Render("x") },
		"statusInsert":        func() string { return s.StatusInsert.Render("x") },
		"statusRead":          func() string { return s.StatusRead.Render("x") },
		"composerActive":      func() string { return s.ComposerActive.Render("x") },
		"composerParked":      func() string { return s.ComposerParked.Render("x") },
		"composerPaneFocused": func() string { return s.ComposerPaneFocused.Render("x") },
	}
	for name, render := range roles {
		if got := render(); got == "" {
			t.Errorf("role %s: rendered empty string", name)
		}
	}
}

// TestStyleRoleTokensCarryExpectedAttributes locks the spec's design
// choices for the role-token table. INSERT-mode pill must carry a
// Background (spec §"Mode-aware status bar": filled for active-input
// modes, outlined for navigational); StatusNormal stays outlined (no
// background). These are the bits a future refactor is most likely to
// drop accidentally.
func TestStyleRoleTokensCarryExpectedAttributes(t *testing.T) {
	t.Parallel()
	s := defaultStyles()
	if !s.Attention.GetBold() {
		t.Error("Attention: spec calls for bold; got Bold=false")
	}
	if !s.HeaderName.GetBold() {
		t.Error("HeaderName: agent name should be bold")
	}
	// INSERT pill is filled (background set); NORMAL is outlined (no background).
	if _, ok := s.StatusInsert.GetBackground().(lipgloss.NoColor); ok {
		t.Error("StatusInsert: spec calls for filled background; got NoColor")
	}
	if _, ok := s.StatusNormal.GetBackground().(lipgloss.NoColor); !ok {
		t.Errorf("StatusNormal: spec says outlined (no background); got %#v", s.StatusNormal.GetBackground())
	}
	// SelectedRow must carry both a left border (the ▌ bar) and a
	// background. The bar+tint together are the selection treatment;
	// dropping either would break the "selected turn pops" affordance.
	if _, ok := s.SelectedRow.GetBackground().(lipgloss.NoColor); ok {
		t.Error("SelectedRow: spec calls for background tint; got NoColor")
	}
	if !s.SelectedRow.GetBorderLeft() {
		t.Error("SelectedRow: spec calls for a left border (▌ bar); got BorderLeft=false")
	}
}
