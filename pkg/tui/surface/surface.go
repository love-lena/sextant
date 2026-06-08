// Package surface is the dash's pane stratum (ADR-0023): the three M4 panes —
// presence, the message stream (with an optional compose), and artifact (a
// document reader/review) — built on the widget toolkit and the busfeed adapter
// against one small contract.
//
// A Surface is a Bubble Tea component that knows how to be a pane: it sizes to
// the inner area the layout grants, takes one of three focus states, renders its
// own content, and emits intents (OpenMsg/DoneMsg) rather than quitting or
// addressing another surface. It declares an id and a title so the layout can
// toggle it. Each surface runs standalone as its own tea.Program AND mounts as a
// pane unchanged — the layout wraps a surface's View in widget.Box, so the
// surface renders inner content only and never owns its own chrome.
//
// widget ⊂ surface ⊂ dash: this package touches only the layer below it — the
// theme, the widgets, the busfeed adapter, and the public SDK (pkg/sextant and
// the public wire atom). It never reaches for NATS or any internal package; a
// go/parser import test enforces that.
package surface

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Surface is the contract the three panes implement (ADR-0023). It is a Bubble
// Tea component (Init/Update/View) extended with the four things a pane needs
// beyond a bare model: a stable id and a human title (so the layout can toggle
// and label it), a SetSize for the inner content area the layout grants, and a
// SetFocus for the three-state focus cue.
//
// Chrome ownership: a Surface renders inner content ONLY. View returns the
// pane's body sized to the last SetSize; the layout draws the widget.Box frame
// and the focus border around it, so focus and title handling stay uniform
// across panes. A surface still receives its Focus (via SetFocus) because the
// content cue — an active cursor, a live compose line — lives inside the body,
// not on the border.
//
// Intents, not calls: a Surface never quits and never addresses another surface.
// When it wants the dash to do something — open a thing in detail, or hand focus
// back — it emits an OpenMsg or a DoneMsg as a tea.Cmd from Update. The dash
// interprets the intent; the surface stays ignorant of the layout.
type Surface interface {
	// ID is the stable identifier the layout toggles a pane by. It is constant
	// for the life of the surface (e.g. "presence", "stream", "artifact").
	ID() string

	// Title is the human label drawn into the pane's chrome.
	Title() string

	// SetSize sets the inner content area (inside any box chrome) the surface
	// renders into — the same convention the widgets use. The layout calls it on
	// every reflow.
	SetSize(w, h int)

	// SetFocus sets the surface's three-state focus: idle (resting), selected
	// (the layout landed on it), or active (the operator stepped in and input is
	// routed here). The surface renders the inside-the-body cue for the state; the
	// layout draws the border.
	SetFocus(widget.Focus)

	// Init starts the surface's work: opening a feed, kicking off an initial
	// fetch, or arming a refresh tick. It is called once when the surface is
	// mounted (or when run standalone, by the host program).
	Init() tea.Cmd

	// Update handles input and events, mutating the surface, and returns any
	// follow-up commands — including the pump step that keeps a feed running and
	// the intents (OpenMsg/DoneMsg) the surface emits. A surface receives input
	// only while it is active; the layout routes keys by focus.
	Update(tea.Msg) tea.Cmd

	// View renders the surface's inner content, sized to the last SetSize. It does
	// not draw box chrome — the layout owns that.
	View() string

	// Stop tears the surface down: it releases any live resource the surface owns
	// (a busfeed subscription, an artifact watch, a refresh tick). The layout calls
	// it exactly once when the surface is unmounted, and a standalone host calls it
	// on quit; it must be safe to call more than once. A surface owning no resource
	// no-ops. Without a contract teardown a dropped pane would leak a feed's blocked
	// Next goroutine until its context is cancelled — fail-loud teardown is
	// uniform and discoverable here instead.
	Stop()
}

// OpenKind classifies what an OpenMsg refers to, so the dash can route the open
// without parsing free-form strings. The set is deliberately small; new kinds
// arrive only when a surface has a real new thing to open.
type OpenKind string

const (
	// OpenArtifact asks the dash to open a named artifact (Ref is the artifact
	// name) — e.g. selecting a document reference in the stream.
	OpenArtifact OpenKind = "artifact"
	// OpenClient asks the dash to open a direct view of a client (Ref is the
	// client id) — e.g. selecting a row in presence to start a direct stream.
	OpenClient OpenKind = "client"
)

// OpenMsg is the "open this thing" intent: a surface emits it to ask the dash to
// reveal something in a detail pane or another surface (detail-on-demand is the
// dash's job, 7.4/7.5 — the surface only names what to open). The payload is
// minimal and typed: a kind plus a reference the dash resolves. A surface never
// opens anything itself, so it cannot address or depend on another surface.
type OpenMsg struct {
	// Kind is what Ref refers to.
	Kind OpenKind
	// Ref is the reference the dash resolves: an artifact name for OpenArtifact, a
	// client id for OpenClient.
	Ref string
}

// DoneMsg is the "I've stepped out" intent: a surface emits it when the operator
// leaves its active state (e.g. Esc out of a compose), so the layout returns
// focus to the layout level. It carries the emitting surface's id so the layout
// knows which pane stepped out without tracking it separately.
type DoneMsg struct {
	// ID is the id of the surface that stepped out.
	ID string
}

// openCmd is the tea.Cmd form of an OpenMsg — the shape a surface returns from
// Update to emit the intent.
func openCmd(kind OpenKind, ref string) tea.Cmd {
	return func() tea.Msg { return OpenMsg{Kind: kind, Ref: ref} }
}

// doneCmd is the tea.Cmd form of a DoneMsg for the surface with the given id.
func doneCmd(id string) tea.Cmd {
	return func() tea.Msg { return DoneMsg{ID: id} }
}

// errorFooter renders a one-line error footer in the alert hue (base08, the
// drain/alert slot), clamped to width and truncated to one row. Every surface
// renders it from its captured error so a failure is visible, never swallowed
// (Sextant's fail-loud discipline). A nil err renders nothing.
func errorFooter(t theme.Theme, err error, w int) string {
	if err == nil {
		return ""
	}
	if w <= 0 {
		w = 1
	}
	return lipgloss.NewStyle().
		Foreground(t.StatusHue(theme.StatusDraining)).
		Width(w).
		MaxWidth(w).
		MaxHeight(1).
		Render("! " + err.Error())
}
