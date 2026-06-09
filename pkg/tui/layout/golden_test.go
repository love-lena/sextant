package layout_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// The golden tests render the cockpit's View deterministically — fixed size,
// fixed mock surfaces, no time or bus — and assert it against committed goldens
// via teatest.RequireEqualOutput. They cover the four AC#4 motions (preset
// switch, pane toggle + reflow, reflow on resize, detail-on-demand) plus the
// layout-level vs stepped-in focus borders. Regenerate with:
//
//	go test ./pkg/tui/layout -update
//
// Rendering View directly (not a full PTY program) keeps the goldens free of
// cursor-positioning ANSI and timing flakiness; it is the same compose code path
// the dash runs. The mock surfaces make the content deterministic and prove the
// layout composes against the Surface contract alone.

const (
	goldenW = 80
	goldenH = 24
)

// cockpit builds a four-pane cockpit (presence/stream/artifact + a detail pane)
// over mock surfaces, sized to the golden terminal.
func cockpit(t *testing.T) layout.Model {
	t.Helper()
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), layout.DefaultConfig(),
		newMock("presence", "presence"),
		newMock("stream", "stream"),
		newMock("artifact", "artifact"),
		newMock("detail", "detail"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: goldenW, Height: goldenH})
	return m
}

func golden(t *testing.T, m layout.Model) {
	t.Helper()
	teatest.RequireEqualOutput(t, []byte(m.View()))
}

// TestPresetGolden covers preset-switch: the three built-in presets, each at the
// layout level with presence selected, so the goldens show the distinct
// arrangements the operator cycles through.
func TestPresetGolden(t *testing.T) {
	for _, name := range []string{"cockpit", "stream", "split"} {
		t.Run(name, func(t *testing.T) {
			m := cockpit(t)
			// Cycle to the named preset (cockpit is the default; p advances).
			for m.Config().Preset != name {
				m, _ = m.Update(key("p"))
			}
			golden(t, m)
		})
	}
}

// TestFocusGolden covers the focus borders: idle (no selection on a pane),
// selected (the layout landed on it), and active (stepped in). presence is the
// selected/active pane.
func TestFocusGolden(t *testing.T) {
	t.Run("layout_level", func(t *testing.T) {
		// presence selected (accent), the rest idle (dim).
		golden(t, cockpit(t))
	})
	t.Run("stepped_in", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(key("enter")) // step into presence → active
		golden(t, m)
	})
	t.Run("selection_moved", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(key("right")) // select stream (spatially right of presence)
		golden(t, m)
	})
}

// TestToggleGolden covers pane-toggle + reflow: a pane turned off and the grid
// reflowed to fill the freed space.
func TestToggleGolden(t *testing.T) {
	m := cockpit(t)
	// Hide artifact via the options menu: move to its row (index 2) and toggle.
	m, _ = m.Update(key("o"))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("down"))
	m, _ = m.Update(key("enter")) // toggle artifact off
	m, _ = m.Update(key("esc"))   // close menu
	golden(t, m)
}

// TestResizeGolden covers reflow on resize: the same cockpit re-fit to a larger
// and a smaller terminal.
func TestResizeGolden(t *testing.T) {
	t.Run("wide", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
		golden(t, m)
	})
	t.Run("narrow", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
		golden(t, m)
	})
}

// TestDetailGolden covers detail-on-demand: hidden (default), then shown (the
// grid reflows to give it a slot, selected + stepped in).
func TestDetailGolden(t *testing.T) {
	t.Run("hidden", func(t *testing.T) {
		golden(t, cockpit(t))
	})
	t.Run("shown", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(key("d")) // show detail
		golden(t, m)
	})
	t.Run("opened_by_intent", func(t *testing.T) {
		m := cockpit(t)
		m, _ = m.Update(surface.OpenMsg{Kind: surface.OpenArtifact, Ref: "design-doc"})
		golden(t, m)
	})
}

// TestOptionsMenuGolden covers the options overlay: the minimal selectable list
// composited over the live cockpit.
func TestOptionsMenuGolden(t *testing.T) {
	m := cockpit(t)
	m, _ = m.Update(key("o"))
	golden(t, m)
}
