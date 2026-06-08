// Package layout is the dash's customization stratum (ADR-0023): the layer that
// composes pane-surfaces into the cockpit the operator controls. It owns the
// btop model — built-in preset arrangements, per-pane on/off toggling, reflow to
// fill the freed space, and a config file that persists the choice — plus
// detail-on-demand (a hidden pane toggled in and out) and the two-level
// focus/navigation interaction.
//
// widget ⊂ surface ⊂ layout ⊂ dash: this package touches only the layer below —
// the theme, the widgets (for the Box chrome and Focus), and the surface
// contract (id/title/SetSize/SetFocus/Update/View/Stop, and the OpenMsg/DoneMsg
// intents). It is domain-free: it never constructs a surface, never reaches for
// the SDK or NATS or any internal package (a go/parser import test enforces
// this). The host (7.5) builds the domain surfaces and hands them to the layout;
// the layout arranges, toggles, focuses, and reflows them, and re-emits a
// surface's OpenMsg so the host can retarget detail content without the layout
// learning what an artifact is.
package layout

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// level is the two-level focus state (ADR-0023's locked interaction model). At
// the layout level the operator moves the *selected* pane (accent border) with
// the nav keys and acts on the layout (toggle, preset, options, quit). Stepping
// in with Enter makes the selected pane *active* and routes keys to its Update;
// stepping out with Esc (or the surface's own DoneMsg) returns to the layout
// level. Exactly one pane is selected at the layout level; exactly one is active
// when stepped in.
type level int

const (
	// levelLayout is the resting level: nav moves the selection, layout keys act
	// on the cockpit.
	levelLayout level = iota
	// levelPane is the stepped-in level: keys route to the active surface.
	levelPane
)

// Model is the cockpit: a tea.Model-shaped root that arranges a set of surfaces
// into a preset layout, toggles panes on and off, reflows to fill, and runs the
// two-level focus machine. The host constructs it with a set of surfaces (it
// never builds surfaces itself) and an initial Config, drives it as a Bubble Tea
// model, and reads back the current Config (Config) to persist on quit.
type Model struct {
	th   theme.Theme
	keys theme.Keymap

	// order is the host's pane order (registration order), the stable order presets
	// fill slots in and the selection cycles through. It includes the detail pane
	// id if a detail surface was supplied.
	order []string
	// surfaces maps pane id → surface. The detail surface (if any) lives here too,
	// keyed by detailPaneID.
	surfaces map[string]surface.Surface
	// hasDetail records whether a detail surface was supplied; detail-on-demand is
	// a no-op without one.
	hasDetail bool

	// preset is the active arrangement name.
	preset string
	// hidden is the set of pane ids toggled off. The detail pane starts hidden and
	// is governed by detailShown rather than this set.
	hidden map[string]bool
	// detailShown is whether the detail-on-demand pane is currently visible. It is
	// hidden by default and toggles in/out; it is never in the always-on visible
	// set.
	detailShown bool
	// detailTarget is the last opaque reference the detail pane was opened on
	// (mirrored into Config). The layout stores it but never resolves it.
	detailTarget string

	// rects is the last computed arrangement: visible pane id → outer Rect. Recomputed
	// on every reflow (toggle, preset switch, resize, detail toggle).
	rects map[string]Rect

	// level is the current focus level (layout vs stepped-in).
	level level
	// selected is the id of the pane the layout selection has landed on. It is
	// always a currently-visible pane; reflow keeps it valid.
	selected string

	// w, h are the terminal size. statusH rows at the bottom are the hint bar.
	w, h int

	// menu is the options overlay; non-nil while open.
	menu *optionsMenu
}

// statusH is the number of rows the bottom hint bar occupies. The panes tile the
// remaining (h - statusH) rows.
const statusH = 1

// New builds a cockpit Model over a set of surfaces, applying an initial Config.
// The surfaces slice is the host's pane order; one surface MAY carry the
// reserved detail id (detailPaneID == "detail"), in which case it becomes the
// detail-on-demand pane (hidden by default). The keymap supplies every binding —
// the layout hardcodes no key. The theme variant in cfg overrides th's variant
// so a persisted theme choice is honoured on open.
func New(th theme.Theme, keys theme.Keymap, cfg Config, surfaces ...surface.Surface) Model {
	m := Model{
		th:       th,
		keys:     keys,
		surfaces: make(map[string]surface.Surface, len(surfaces)),
		hidden:   make(map[string]bool),
		rects:    make(map[string]Rect),
	}
	for _, s := range surfaces {
		id := s.ID()
		if _, dup := m.surfaces[id]; dup {
			continue // first registration of an id wins; ignore a duplicate
		}
		m.surfaces[id] = s
		m.order = append(m.order, id)
		if id == detailPaneID {
			m.hasDetail = true
		}
	}
	m.apply(cfg)
	return m
}

// apply sets the layout's state from a Config: the theme variant, the active
// preset, the hidden set, and the detail target. An unknown preset falls back to
// the cockpit default; the detail pane id is never put in the hidden set (its
// visibility is detailShown, governed by detail-on-demand, not the toggle set).
func (m *Model) apply(cfg Config) {
	if cfg.Theme == theme.VariantLight || cfg.Theme == theme.VariantDark {
		m.th = theme.New(cfg.Theme)
	}
	m.preset = cfg.Preset
	if !validPreset(m.preset) {
		m.preset = PresetCockpit
	}
	m.hidden = make(map[string]bool)
	for _, id := range cfg.Hidden {
		if id == detailPaneID {
			continue
		}
		if _, ok := m.surfaces[id]; ok {
			m.hidden[id] = true
		}
	}
	m.detailTarget = cfg.DetailTarget
	// A loaded config does not auto-show detail; detail-on-demand stays hidden until
	// the operator toggles it or a surface intent opens it.
	m.detailShown = false
	m.selected = m.firstVisible()
}

// Config snapshots the layout's current state as a Config the host can persist.
// It records the active preset, the hidden set, the theme variant, and the
// detail target; Placements stays empty (preset-mode). The host calls this on
// change or on quit and hands the result to SaveConfig.
func (m Model) Config() Config {
	cfg := DefaultConfig()
	cfg.Preset = m.preset
	cfg.Theme = m.th.Variant
	cfg.DetailTarget = m.detailTarget
	cfg.Hidden = m.hiddenList()
	return cfg
}

// hiddenList returns the hidden pane ids in the host's registration order, so a
// saved config is stable (a map iteration order would churn the file).
func (m Model) hiddenList() []string {
	var out []string
	for _, id := range m.order {
		if m.hidden[id] {
			out = append(out, id)
		}
	}
	return out
}

// Init starts every mounted surface (the layout owns mounting) and sizes the
// initial (empty) arrangement. The host typically sends a WindowSizeMsg right
// after, which triggers the first real reflow.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range m.order {
		if c := m.surfaces[id].Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// View renders the cockpit: every visible pane drawn in its reflowed Rect with
// the shared widget.Box chrome (the layout owns the chrome; surfaces render
// inner content only), composited onto a blank canvas, with the hint bar along
// the bottom and the options menu overlaid when open.
func (m Model) View() string {
	if m.w == 0 || m.h == 0 {
		return "starting dash…"
	}
	canvas := newCanvas(m.w, m.areaH(), m.th.Bg)
	// Place panes in the stable visible order (not the map's randomized iteration
	// order) so the composite is deterministic — goldens depend on it, and two
	// boxes that share canvas rows must splice in a fixed order.
	for _, id := range m.visibleOrder() {
		r, ok := m.rects[id]
		if !ok {
			continue
		}
		s := m.surfaces[id]
		focus := m.focusOf(id)
		box := widget.Box(m.th, focus, s.Title(), m.titleHue(s), s.View(), r.W, r.H)
		canvas.place(box, r.X, r.Y)
	}
	body := canvas.render()
	out := lipgloss.JoinVertical(lipgloss.Left, body, m.statusBar())
	if m.menu != nil {
		return m.menu.overlay(m.th, out, m.w, m.h)
	}
	return out
}

// areaH is the height available to panes: the terminal height minus the hint bar.
func (m Model) areaH() int {
	h := m.h - statusH
	if h < 1 {
		h = 1
	}
	return h
}

// focusOf returns a pane's three-state focus from the layout state: idle unless
// it is the selected pane, then selected at the layout level or active when
// stepped in.
func (m Model) focusOf(id string) widget.Focus {
	if id != m.selected {
		return widget.FocusIdle
	}
	if m.level == levelPane {
		return widget.FocusActive
	}
	return widget.FocusSelected
}

// titleHue tints a pane's chrome by what the pane is, mirroring the gallery's
// convention so the cockpit reads the same as the surface previews.
func (m Model) titleHue(s surface.Surface) lipgloss.Color {
	switch s.ID() {
	case "presence":
		return m.th.RoleHue(theme.RoleHuman)
	case "artifact", detailPaneID:
		return m.th.KindHue(theme.KindArtifactUpdate)
	case "stream":
		return m.th.KindHue(theme.KindChat)
	default:
		return m.th.Accent
	}
}

// Stop tears down every mounted surface (the Surface contract's teardown), so a
// feed-backed pane releases its pump cleanly. The host calls it on quit; it is
// safe to call once.
func (m Model) Stop() {
	for _, id := range m.order {
		m.surfaces[id].Stop()
	}
}
