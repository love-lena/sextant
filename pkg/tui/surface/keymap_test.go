package surface_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
)

// TestSurfaceStepOutIsOverridable proves the surfaces route their own keys
// through the keymap (keys are data, ADR-0023's locked discipline), not literal
// strings: a remapped step-out (Back) binding drives the step-out by the new
// key, and the default Esc no longer does. It mirrors the layout's
// TestLayoutShortcutsAreOverridable, applied at the surface level — the level the
// final review found still comparing literal "esc"/"enter" strings.
//
// Each surface's active step-out emits a DoneMsg; the test asserts the rebound
// key produces one and the old default produces nothing.
func TestSurfaceStepOutIsOverridable(t *testing.T) {
	// Remap step-out (Back) from the default Esc to F1.
	keys := theme.DefaultKeymap().Merge(
		theme.Override{Action: "Back", Keys: []string{"f1"}},
	)
	f1 := tea.KeyMsg{Type: tea.KeyF1}
	esc := tea.KeyMsg{Type: tea.KeyEsc}

	for _, tc := range []struct {
		name string
		make func() surface.Surface
	}{
		{"stream", func() surface.Surface {
			return surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), keys, surface.WithCompose())
		}},
		{"clients_browser", func() surface.Surface {
			return surface.NewClientsBrowser(context.Background(), nil, theme.Dark(), keys)
		}},
		{"artifact", func() surface.Surface {
			return surface.NewArtifact(context.Background(), nil, "dash-plan", theme.Dark(), keys, surface.WithReview())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.make()
			s.SetSize(40, 8)
			s.SetFocus(widget.FocusActive)

			// The old default key is inert after the rebind.
			if cmd := s.Update(esc); cmd != nil {
				if _, ok := cmd().(surface.DoneMsg); ok {
					t.Fatal("default esc should be inert after remapping Back")
				}
			}

			// The remapped key steps out (emits a DoneMsg carrying the surface id).
			cmd := s.Update(f1)
			if cmd == nil {
				t.Fatal("remapped step-out (f1) produced no command")
			}
			done, ok := cmd().(surface.DoneMsg)
			if !ok {
				t.Fatalf("remapped step-out (f1) did not emit a DoneMsg; got %T", cmd())
			}
			if done.ID != s.ID() {
				t.Errorf("DoneMsg carried id %q, want %q", done.ID, s.ID())
			}
		})
	}
}

// TestSurfaceSendIsOverridable proves the stream's send/confirm action is read
// from the keymap too: rebinding Enter drives the publish by the new key. With a
// nil client a real publish would panic, so the test asserts only that the
// composed line is consumed (the input clears) on the rebound key and not on the
// old default — enough to prove the binding, not the literal string, gates send.
func TestSurfaceSendIsOverridable(t *testing.T) {
	keys := theme.DefaultKeymap().Merge(
		theme.Override{Action: "Enter", Keys: []string{"ctrl+s"}},
	)
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), keys, surface.WithCompose())
	s.SetSize(40, 8)
	s.SetFocus(widget.FocusActive)

	// Type a line, then press the OLD default enter: it must not send (so the line
	// stays in the compose buffer, visible in the view).
	typeInto(s, "ship the dash")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(stripANSI(s.View()), "ship the dash") {
		t.Fatal("default enter should be inert after remapping Enter (the line should still be composing)")
	}

	// The remapped send key returns a publish command (the surface acted on it).
	cmd := s.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("remapped send (ctrl+s) produced no command")
	}
	// The input clears on send; the composed text is no longer in the view.
	if strings.Contains(stripANSI(s.View()), "ship the dash") {
		t.Error("remapped send did not clear the compose buffer")
	}
}

// TestSurfaceSetThemeRetheme proves SetTheme re-themes a surface in place: the
// rendered output (hue ANSI included) changes when the theme variant flips, so a
// runtime theme toggle actually re-themes the pane body — not just the chrome the
// layout owns. The visible text is unchanged; only the hues differ, so the test
// compares the raw (un-stripped) render and the stripped render separately.
func TestSurfaceSetThemeRetheme(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() surface.Surface
		seed func(surface.Surface)
	}{
		{
			"clients_browser",
			func() surface.Surface {
				return surface.NewClientsBrowser(context.Background(), nil, theme.Dark(), theme.DefaultKeymap())
			},
			func(s surface.Surface) { s.Update(surface.ClientsLoadedMsg{Clients: sampleClients()}) },
		},
		{
			"artifacts_browser",
			func() surface.Surface {
				return surface.NewArtifactsBrowser(context.Background(), nil, theme.Dark(), theme.DefaultKeymap())
			},
			func(s surface.Surface) { s.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()}) },
		},
		{
			"stream",
			func() surface.Surface {
				return surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
			},
			func(s surface.Surface) { feedStream(s.(*surface.Stream)) },
		},
		{
			"artifact",
			func() surface.Surface {
				return surface.NewArtifact(context.Background(), nil, "dash-plan", theme.Dark(), theme.DefaultKeymap())
			},
			func(s surface.Surface) { s.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()}) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.make()
			s.SetSize(40, 10)
			s.SetFocus(widget.FocusSelected)
			tc.seed(s)

			dark := s.View()
			s.SetTheme(theme.Light())
			light := s.View()

			// The same visible text, re-hued: the renders differ (proving the new
			// palette is applied) while the stripped text is unchanged.
			if dark == light {
				t.Error("SetTheme did not change the rendered hues")
			}
			if stripANSI(dark) != stripANSI(light) {
				t.Errorf("SetTheme changed the visible text, not just the hues:\n dark: %q\nlight: %q", stripANSI(dark), stripANSI(light))
			}
		})
	}
}
