package dash

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/surface"
)

// root is the dash's Bubble Tea root model: it wraps the layout.Model (the
// cockpit) and is the HOST end of the contracts the layer below leaves to the
// dash (ADR-0023, 7.5). It owns only the few things a host owns; the pane
// mechanics (focus machine, reflow, toggling, the surface routing) all live in
// the layout:
//
//   - resize: a tea.WindowSizeMsg is forwarded into the layout, which reflows
//     every visible pane.
//   - detail-on-demand retarget: it CONSUMES layout.DetailOpenedMsg to point the
//     detail reader at the named artifact, and does NOT feed that message back
//     into the layout — the loop contract. DetailOpenedMsg is a distinct type the
//     layout would re-open on if it were the raw OpenMsg; the host never re-emits
//     one, so forwarding every other message into the layout is safe (the layout
//     ignores DetailOpenedMsg).
//   - drain: a watch on client.Drained() winds the dash down cleanly when the bus
//     drains under it (the standard-client contract — a cooperative bus drain
//     becomes a clean quit).
//
// Surface teardown happens through the layout: every quit path in the layout
// (the quit key, ctrl+c, the options-menu quit) calls layout.Stop, and the
// drain path here calls it too. The remaining host duties — persisting the
// layout config and closing the client — run in Run after the program exits, so
// they happen exactly once regardless of which quit path fired.
type root struct {
	m      layout.Model
	client *sextant.Client
	detail *detailSurface // the retargetable detail pane (nil if none mounted)
}

// drainedMsg is the internal quit trigger the Drained watch emits, distinct so
// the root can tell a cooperative bus drain apart from an operator quit.
type drainedMsg struct{}

// newRoot builds the root over an assembled cockpit, the held client, and the
// detail pane (so the host can retarget it). The configPath is not held here —
// Run owns config persistence after the program exits.
func newRoot(m layout.Model, client *sextant.Client, detail *detailSurface) root {
	return root{m: m, client: client, detail: detail}
}

// Init starts the layout (which mounts and Inits every surface) and arms the
// drain watch. The host program sends a WindowSizeMsg right after, triggering
// the first reflow.
func (r root) Init() tea.Cmd {
	return tea.Batch(r.m.Init(), r.watchDrain())
}

// watchDrain blocks on the client's Drained channel off the main loop and
// returns a drainedMsg when the bus drains. It never busy-waits — the channel
// close is the only wakeup — so an idle dash holds one parked goroutine, no
// poll.
func (r root) watchDrain() tea.Cmd {
	ch := r.client.Drained()
	return func() tea.Msg {
		<-ch
		return drainedMsg{}
	}
}

// Update routes the host-owned messages and forwards everything else into the
// layout. It honours the detail-on-demand loop contract: DetailOpenedMsg is
// consumed here and never forwarded back into the layout.
func (r root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case layout.DetailOpenedMsg:
		// The layout has already shown + focused the detail pane; the host resolves
		// the ref. An artifact ref retargets the detail reader onto it. A client ref
		// has no M4 direct-stream surface (that is M5), so it is acknowledged and the
		// detail pane the layout opened keeps its current artifact.
		// CRITICAL: do NOT feed this message back into the layout (the loop contract).
		if r.detail != nil && msg.Kind == surface.OpenArtifact {
			return r, r.detail.Retarget(msg.Ref)
		}
		return r, nil

	case drainedMsg:
		// The bus drained under us: tear the surfaces down and quit. Config + client
		// close run in Run after the program exits.
		r.m.Stop()
		return r, tea.Quit
	}

	// Everything else (keys, resize, feed events, ticks) flows into the layout. The
	// layout owns the focus machine, reflow, and surface routing — including its own
	// quit paths, which call layout.Stop before returning tea.Quit.
	var cmd tea.Cmd
	r.m, cmd = r.m.Update(msg)
	return r, cmd
}

// View renders the cockpit.
func (r root) View() string { return r.m.View() }
