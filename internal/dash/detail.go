package dash

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// detailSurface is the cockpit's detail-on-demand pane: an artifact reader
// mounted under the layout's reserved "detail" id (the artifact surface is the
// natural detail target, ADR-0023/7.5). It wraps a surface.Artifact and reports
// the reserved id so the layout treats it as the hidden, toggled detail pane,
// while the inner Artifact does the real reading.
//
// It is retargetable: when the host receives a layout.DetailOpenedMsg naming a
// new artifact, it calls Retarget to rebuild the inner reader on that name. A
// surface.Artifact resolves its name + hues at construction and exposes no
// rename, so retarget rebuilds it cleanly — stopping the old reader's resources
// and re-running the new one's Init/size/focus — rather than mutating a field
// the surface never meant to be live. This is composition-root work, not a new
// surface type or SDK surface.
type detailSurface struct {
	// ctx, client, theme, keys are the construction inputs the inner Artifact
	// needs, held so Retarget can rebuild it on a new name.
	ctx    context.Context
	client *sextant.Client
	theme  theme.Theme
	keys   theme.Keymap

	inner *surface.Artifact

	// w, h, focus are the last layout-granted geometry/focus, replayed onto a
	// rebuilt inner reader so a retarget lands sized and focused exactly as the
	// pane it replaced.
	w, h    int
	focus   widget.Focus
	started bool // Init has run; a rebuilt reader is (re-)Init'd to match
}

// detailPaneID is the reserved layout id for the detail-on-demand pane (mirrors
// layout.detailPaneID, which is unexported). The host mounts the detail surface
// under this id so the layout governs it as detail-on-demand.
const detailPaneID = "detail"

// newDetail builds the detail pane as an artifact reader for an initial name
// (typically the cockpit's seeded document, so opening detail shows something
// real on first toggle). The inner reader does no I/O until Init.
func newDetail(ctx context.Context, client *sextant.Client, name string, th theme.Theme, keys theme.Keymap) *detailSurface {
	return &detailSurface{
		ctx:    ctx,
		client: client,
		theme:  th,
		keys:   keys,
		inner:  surface.NewArtifact(ctx, client, name, th, keys),
	}
}

// Retarget rebuilds the inner reader on a new artifact name and returns the
// Init command to load it. It tears down the old reader's resources first, then
// replays the last size/focus so the rebuilt pane renders in place. The host
// calls it from its DetailOpenedMsg handler — the layout has already shown and
// focused the detail pane by then, so Retarget only swaps the content.
func (d *detailSurface) Retarget(name string) tea.Cmd {
	d.inner.Stop()
	d.inner = surface.NewArtifact(d.ctx, d.client, name, d.theme, d.keys)
	d.inner.SetSize(d.w, d.h)
	d.inner.SetFocus(d.focus)
	if d.started {
		return d.inner.Init()
	}
	return nil
}

// ID reports the reserved detail id (not the inner Artifact's "artifact" id), so
// the layout mounts this as the detail-on-demand pane.
func (d *detailSurface) ID() string { return detailPaneID }

// Title is the inner reader's title (the artifact name), so the detail pane
// labels what it is showing.
func (d *detailSurface) Title() string { return d.inner.Title() }

// SetSize records the geometry (for replay across a retarget) and forwards it.
func (d *detailSurface) SetSize(w, h int) {
	d.w, d.h = w, h
	d.inner.SetSize(w, h)
}

// SetFocus records the focus (for replay across a retarget) and forwards it.
func (d *detailSurface) SetFocus(f widget.Focus) {
	d.focus = f
	d.inner.SetFocus(f)
}

// SetTheme records the theme (so a later Retarget rebuilds the inner reader on
// the current theme) and forwards it to the inner reader, so a runtime theme
// switch re-themes the detail pane body too.
func (d *detailSurface) SetTheme(th theme.Theme) {
	d.theme = th
	d.inner.SetTheme(th)
}

// Init starts the inner reader and records that the pane is live, so a later
// Retarget knows to Init the rebuilt reader too.
func (d *detailSurface) Init() tea.Cmd {
	d.started = true
	return d.inner.Init()
}

// Update forwards to the inner reader.
func (d *detailSurface) Update(msg tea.Msg) tea.Cmd { return d.inner.Update(msg) }

// View renders the inner reader's content.
func (d *detailSurface) View() string { return d.inner.View() }

// Stop tears the inner reader down (the Surface contract's teardown).
func (d *detailSurface) Stop() { d.inner.Stop() }

var _ surface.Surface = (*detailSurface)(nil)
