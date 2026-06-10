// Package layout is the dash's customization stratum (ADR-0023, refined by
// ADR-0024 and ADR-0026): the layer that composes pane-surfaces into the
// cockpit the operator controls. It owns the btop model — built-in preset
// arrangements, per-pane on/off toggling, reflow to fill the freed space, and a
// config file that persists the choice — plus the tmux-style focus model: one
// pane is focused at all times, keys go to the focused pane, and moving focus
// never changes what a pane shows.
//
// The layout composes plain panes only. Detail-on-demand is realized INSIDE
// each pane (ADR-0024: a browser opens its detail in place and pops back with
// Esc — list-versus-detail is a surface's own state), so the layout has no
// detail pane, no retarget flow, and no detail toggle.
//
// widget ⊂ surface ⊂ layout ⊂ dash: this package touches only the layer below —
// the theme, the widgets (for the Box chrome and Focus), and the surface
// contract (id/title/SetSize/SetFocus/CapturingText/Update/View/Stop). It is
// domain-free: it never constructs a surface, never reaches for the SDK or NATS
// or any internal package (a go/parser import test enforces this). The host
// builds the domain surfaces and hands them to the layout; the layout arranges,
// toggles, focuses, and reflows them.
package layout

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// Model is the cockpit: a tea.Model-shaped root that arranges a set of surfaces
// into a preset layout, toggles panes on and off, reflows to fill, and holds
// the one focused pane (ADR-0026). The host constructs it with a set of
// surfaces (it never builds surfaces itself) and an initial Config, drives it
// as a Bubble Tea model, and reads back the current Config (Config) to persist
// on quit.
type Model struct {
	th   theme.Theme
	keys theme.Keymap

	// order is the host's pane order (registration order), the stable order presets
	// fill slots in and the selection cycles through.
	order []string
	// surfaces maps pane id → surface.
	surfaces map[string]surface.Surface

	// preset is the active arrangement name.
	preset string
	// hidden is the set of pane ids toggled off.
	hidden map[string]bool

	// rects is the last computed arrangement: visible pane id → outer Rect. Recomputed
	// on every reflow (toggle, preset switch, resize).
	rects map[string]Rect

	// focused is the id of the focused pane — the one pane keys are delivered to
	// (ADR-0026: exactly one pane is focused at all times). It is always a
	// currently-visible pane; reflow keeps it valid.
	focused string

	// w, h are the terminal size. statusH rows at the bottom are the hint bar.
	w, h int

	// menu is the options overlay; non-nil while open.
	menu *optionsMenu
}

// statusH is the number of rows the bottom hint bar occupies. The panes tile the
// remaining (h - statusH) rows.
const statusH = 1

// New builds a cockpit Model over a set of surfaces, applying an initial Config.
// The surfaces slice is the host's pane order. The keymap supplies every binding
// — the layout hardcodes no key. The theme variant in cfg overrides th's variant
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
	}
	m.apply(cfg)
	return m
}

// apply sets the layout's state from a Config: the theme variant, the active
// preset, and the hidden set. An unknown preset falls back to the cockpit
// default.
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
		if _, ok := m.surfaces[id]; ok {
			m.hidden[id] = true
		}
	}
	// Focus starts on the first visible pane (ADR-0026: one pane is always
	// focused; there is no resting layout level).
	m.focused = m.firstVisible()
}

// Config snapshots the layout's current state as a Config the host can persist.
// It records the active preset, the hidden set, and the theme variant;
// Placements stays empty (preset-mode). The host calls this on change or on
// quit and hands the result to SaveConfig.
func (m Model) Config() Config {
	cfg := DefaultConfig()
	cfg.Preset = m.preset
	cfg.Theme = m.th.Variant
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
	// No rects means no panes to composite — two distinct states, each named
	// honestly: every pane toggled off (the operator's choice; say so and point
	// at the options menu, which stays reachable to toggle panes back on) or a
	// terminal too small to fit even one pane at the Box minimum (graceful
	// degradation; the minimum usable terminal is one pane plus the hint row).
	// The options menu still overlays either notice, so the escape hatch is
	// visible, not just live.
	if len(m.rects) == 0 {
		notice := m.tooSmallNotice()
		if len(m.visibleOrder()) == 0 {
			notice = m.allHiddenNotice()
		}
		if m.menu != nil {
			return m.menu.overlay(m.th, notice, m.w, m.h)
		}
		return notice
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

// tooSmallNotice renders the degraded message shown when the terminal can't fit
// even one pane at the Box minimum: a centred note stating the minimum size, on
// the theme background. The minimum usable terminal is one pane (minPaneW ×
// minPaneH) plus the one-row hint bar.
func (m Model) tooSmallNotice() string {
	const (
		needW = minPaneW
		needH = minPaneH + statusH
	)
	return m.notice(fmt.Sprintf("terminal too small\nneed ≥ %d×%d", needW, needH))
}

// allHiddenNotice renders the message shown when the operator has toggled every
// pane off: an honest statement of the state plus the way back in, never a
// misdiagnosis (an empty arrangement is not a too-small terminal) and never a
// refusal — hiding the last pane stays allowed; the notice keeps it recoverable.
// The hint names the Options key from the keymap, so a rebind stays honest.
func (m Model) allHiddenNotice() string {
	return m.notice(fmt.Sprintf("all panes hidden\npress %s for options", m.keys.Options.Help().Key))
}

// notice renders a full-terminal centred message on the theme background — the
// shared chrome for the no-rects states (all panes hidden, terminal too small).
func (m Model) notice(msg string) string {
	return lipgloss.NewStyle().
		Background(m.th.Bg).
		Foreground(m.th.Fg).
		Width(m.w).
		Height(m.h).
		MaxWidth(m.w).
		MaxHeight(m.h).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}

// focusOf returns a visible pane's three-state focus from the layout state
// (ADR-0026): the focused pane is active (keys are routed to it); every other
// visible pane is selected — the muted-cursor state that keeps its place
// readable while unfocused. applyFocus sets hidden/unmounted panes idle before
// calling this.
func (m Model) focusOf(id string) widget.Focus {
	if id == m.focused {
		return widget.FocusActive
	}
	return widget.FocusSelected
}

// titleHue tints a pane's chrome by what the pane is, mirroring the gallery's
// convention so the cockpit reads the same as the surface previews. It is keyed
// off the STABLE pane id, never the title: a browser's Title changes live while
// a detail is open ("Topics" → "Topic · build") and the chrome label tracks it,
// but the hue stays the pane's own so stepping into a detail never recolours
// the frame.
func (m Model) titleHue(s surface.Surface) lipgloss.Color {
	switch s.ID() {
	case "clients":
		return m.th.RoleHue(theme.RoleHuman)
	case "topics":
		return m.th.KindHue(theme.KindChat)
	case "artifacts":
		return m.th.KindHue(theme.KindArtifactUpdate)
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
