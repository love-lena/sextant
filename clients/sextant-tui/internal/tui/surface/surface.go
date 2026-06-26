// Package surface is the dash's pane stratum (ADR-0023, refined by ADR-0024
// and ADR-0026): the three master-detail browsers — clients, topics, artifacts
// — and the two detail surfaces they open in place (the message stream and the
// artifact reader), built on the widget toolkit and the busfeed adapter against
// one small contract.
//
// A Surface is a Bubble Tea component that knows how to be a pane: it sizes to
// the inner area the layout grants, takes one of three focus states, renders
// its own content, and never quits or addresses another surface. It declares an
// id and a title so the layout can toggle it. Each surface runs standalone as
// its own tea.Program AND mounts as a pane unchanged — the layout wraps a
// surface's View in widget.Box, so the surface renders inner content only and
// never owns its own chrome.
//
// Navigation is content state (ADR-0026): Enter opens the selected row's detail
// in place, Esc pops one level back to the list, and Esc at a surface's top
// level does nothing — leaving a pane is the host's focus move, not a level,
// so an open detail holds its place while the operator works elsewhere.
//
// widget ⊂ surface ⊂ dash: this package touches only the layer below it — the
// theme, the widgets, the busfeed adapter, and the public SDK (pkg/sextant and
// the public wire atom). It never reaches for NATS or any internal package; a
// go/parser import test enforces that.
package surface

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
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
// A Surface never quits and never addresses another surface; the host owns
// focus and quitting (ADR-0026: ctrl+c always quits, q quits unless the focused
// surface is capturing text — the CapturingText method is how the host asks).
// Opening a detail is a surface's OWN state (ADR-0024: a browser opens a row's
// detail inside its own pane), never an intent to the host; each Back (Esc)
// pops exactly one level, and Back at the top level is a no-op — focus moves,
// not Esc, are how the operator leaves a pane.
type Surface interface {
	// ID is the stable identifier the layout toggles a pane by. It is constant
	// for the life of the surface (e.g. "clients", "topics", "artifacts").
	ID() string

	// Title is the human label drawn into the pane's chrome.
	Title() string

	// SetSize sets the inner content area (inside any box chrome) the surface
	// renders into — the same convention the widgets use. The layout calls it on
	// every reflow.
	SetSize(w, h int)

	// SetFocus sets the surface's three-state focus: idle (hidden/unmounted),
	// selected (visible but unfocused — the muted cue that keeps the pane's
	// place readable), or active (the focused pane; input is routed here). The
	// surface renders the inside-the-body cue for the state; the layout draws
	// the border.
	SetFocus(widget.Focus)

	// CapturingText reports whether the surface is currently capturing typed
	// text (a focused compose/comment input), so the host knows a printable key
	// must be delivered here rather than acted on as a shortcut (ADR-0026: q
	// quits only from a pane that is not capturing; while a compose is
	// capturing, q types a q). The fail-safe is to return true when in doubt —
	// delivering a key to the surface is recoverable, quitting underneath
	// typing is not. A surface with no text input returns false; a browser
	// delegates to its open detail.
	CapturingText() bool

	// SetTheme re-themes the surface in place: it re-resolves the hues the surface
	// renders in (and rebuilds any palette-dependent renderer it holds, e.g. the
	// artifact reader's Markdown renderer) against the new theme. The layout calls
	// it on every mounted surface when the operator switches theme, so a runtime
	// theme toggle re-themes the pane bodies, not just the chrome the layout owns.
	// The widgets a surface holds take the theme at render time, so re-theming is
	// just storing the new theme and re-rendering any cached output.
	SetTheme(theme.Theme)

	// Init starts the surface's work: opening a feed, kicking off an initial
	// fetch, or arming a refresh tick. It is called once when the surface is
	// mounted (or when run standalone, by the host program).
	Init() tea.Cmd

	// Update handles input and events, mutating the surface, and returns any
	// follow-up commands — including the pump step that keeps a feed running. A
	// surface receives input only while it is the focused (active) pane; the
	// layout routes keys by focus.
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

// The surfaces satisfy the contract: the three ADR-0024 browsers (each embeds
// Browser, itself a Surface) and the two detail surfaces they open. These
// compile-time assertions keep the guarantee in the package itself, independent
// of any host (the gallery, the dash binary), so the contract holds even if
// every caller is removed.
var (
	_ Surface = (*Stream)(nil)
	_ Surface = (*Artifact)(nil)

	_ Surface = (*Browser)(nil)
	_ Surface = (*ClientsBrowser)(nil)
	_ Surface = (*ArtifactsBrowser)(nil)
	_ Surface = (*TopicsBrowser)(nil)
)

// isTextKey reports whether a key is printable text destined for a compose
// (runes or space), as opposed to a control or navigation key. While a surface
// is capturing text, EVERY text key must reach the input — including letters
// that happen to share a binding with a navigation action (j/k are bound
// alongside the arrows, q is the host's quit) — so the key routing checks this
// before any binding match.
func isTextKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace
}

// isTextChunk reports whether a key message is burst/pasted text rather than a
// single keystroke: a bracketed paste (even a single character) or a multi-rune
// KeyRunes chunk. Such a chunk must never be matched against bindings — it is
// content, period. The predicate itself lives in widget (the lowest stratum);
// this is the surface stratum applying the same discipline to its own matches.
func isTextChunk(msg tea.KeyMsg) bool {
	return widget.IsTextChunk(msg)
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
