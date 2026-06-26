package surface_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
)

// TestThemeSwitchKeepsErrorFooter pins the SetTheme/relayout interaction in
// both directory browsers: SetTheme re-applies the last snapshot (which
// restores the full list height, as a snapshot clears the error reservation)
// and then restores the captured error — so without re-running the relayout
// the View emits one row too many and the Box clips the bottom row, which is
// the error footer. The list fills the pane exactly (the clipping case), an
// error footer is showing, and it must still be visible after a runtime theme
// switch. The render goes through widget.Box WITHOUT an interleaved SetSize,
// because a resize would re-run the relayout and mask the bug.
func TestThemeSwitchKeepsErrorFooter(t *testing.T) {
	th := theme.Dark()

	t.Run("clients", func(t *testing.T) {
		cb := surface.NewClientsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		iw, ih := innerOf(30, 7) // inner h = 5: the 5-client list fills the pane
		cb.SetSize(iw, ih)
		cb.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})
		cb.Update(surface.NewClientsErrMsg(errors.New("bus unreachable")))
		render := func(t2 theme.Theme) string {
			return ansi.Strip(widget.Box(t2, widget.FocusSelected, cb.Title(), t2.Fg, cb.View(), 30, 7))
		}
		if !strings.Contains(render(th), "bus unreachable") {
			t.Fatal("precondition: the error footer should be visible before the theme switch")
		}
		cb.SetTheme(theme.Light())
		if out := render(theme.Light()); !strings.Contains(out, "bus unreachable") {
			t.Errorf("the theme switch clipped the clients error footer:\n%s", out)
		}
	})

	t.Run("artifacts", func(t *testing.T) {
		ab := surface.NewArtifactsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		iw, ih := innerOf(32, 5) // inner h = 3: the 3-artifact list fills the pane
		ab.SetSize(iw, ih)
		ab.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()})
		ab.Update(surface.NewArtifactsErrMsg(errors.New("artifact.list failed")))
		render := func(t2 theme.Theme) string {
			return ansi.Strip(widget.Box(t2, widget.FocusSelected, ab.Title(), t2.Fg, ab.View(), 32, 5))
		}
		if !strings.Contains(render(th), "artifact.list failed") {
			t.Fatal("precondition: the error footer should be visible before the theme switch")
		}
		ab.SetTheme(theme.Light())
		if out := render(theme.Light()); !strings.Contains(out, "artifact.list failed") {
			t.Errorf("the theme switch clipped the artifacts error footer:\n%s", out)
		}
	})
}
