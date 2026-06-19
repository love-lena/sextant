package surface_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/surface"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/widget"
)

// TestTopLevelBackIsNoOp pins ADR-0026: Esc (Back) at a surface's TOP level does
// nothing — it produces no command and discards no state. Leaving a pane is the
// host's focus move, not a key the surface acts on; only an open detail's
// hosting browser consumes Esc (to pop one level). The stream additionally
// proves no state is lost: a half-composed line survives the Esc.
func TestTopLevelBackIsNoOp(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}

	t.Run("stream_keeps_compose", func(t *testing.T) {
		s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithCompose())
		s.SetSize(40, 8)
		s.SetFocus(widget.FocusActive)
		typeInto(s, "half a thought")
		if cmd := s.Update(esc); cmd != nil {
			t.Fatalf("top-level esc should produce no command, got %#v", cmd())
		}
		if !strings.Contains(stripANSI(s.View()), "half a thought") {
			t.Error("top-level esc discarded the composed line; it must keep its place")
		}
	})

	t.Run("browser_list", func(t *testing.T) {
		b := surface.NewClientsBrowser(context.Background(), nil, theme.Dark(), theme.DefaultKeymap())
		b.SetSize(40, 8)
		b.SetFocus(widget.FocusActive)
		b.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})
		if cmd := b.Update(esc); cmd != nil {
			t.Fatalf("esc at the list should produce no command, got %#v", cmd())
		}
		if got := b.Title(); got != "Clients" {
			t.Errorf("esc at the list changed the browser state: title %q", got)
		}
	})

	t.Run("artifact_reader", func(t *testing.T) {
		a := surface.NewArtifact(context.Background(), nil, "dash-plan", theme.Dark(), theme.DefaultKeymap(), surface.WithReview())
		a.SetSize(40, 8)
		a.SetFocus(widget.FocusActive)
		if cmd := a.Update(esc); cmd != nil {
			t.Fatalf("top-level esc should produce no command, got %#v", cmd())
		}
	})
}

// TestBrowserDetailPopIsOverridable proves the browser routes its pop key
// through the keymap (keys are data, ADR-0023's locked discipline), not literal
// strings: with Back remapped, the old Esc no longer pops an open detail (it is
// delivered to the detail instead) and the new key does.
func TestBrowserDetailPopIsOverridable(t *testing.T) {
	keys, err := theme.DefaultKeymap().Merge(
		theme.Override{Action: "Back", Keys: []string{"f1"}},
	)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	b := surface.NewClientsBrowser(context.Background(), nil, theme.Dark(), keys)
	b.SetSize(40, 8)
	b.SetFocus(widget.FocusActive)
	b.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})

	b.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open the cursor row's DM detail
	if got := b.Title(); got == "Clients" {
		t.Fatal("precondition: Enter should open a detail (title should track it)")
	}
	opened := b.Title()

	// The old default key no longer pops (it is delivered to the inner detail).
	b.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := b.Title(); got != opened {
		t.Fatalf("default esc should be inert after remapping Back; title went %q → %q", opened, got)
	}

	// The remapped key pops one level back to the list.
	b.Update(tea.KeyMsg{Type: tea.KeyF1})
	if got := b.Title(); got != "Clients" {
		t.Errorf("remapped pop (f1) did not pop the detail; title %q", got)
	}
}

// TestComposeCapturesEveryPrintableKey pins the capture rule (ADR-0026): while
// a compose is capturing, EVERY printable key is text — including the letters
// that share a binding with a navigation action (j and k are bound alongside
// the arrows, q is the host's quit). The full alphabet must land in the input
// verbatim, in both composing surfaces.
func TestComposeCapturesEveryPrintableKey(t *testing.T) {
	const alphabet = "qwertyuiopasdfghjklzxcvbnm"

	t.Run("stream_compose", func(t *testing.T) {
		s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithCompose())
		s.SetSize(60, 8)
		s.SetFocus(widget.FocusActive)
		typeInto(s, alphabet)
		if !strings.Contains(stripANSI(s.View()), alphabet) {
			t.Errorf("compose dropped printable keys; view:\n%s", stripANSI(s.View()))
		}
	})

	t.Run("artifact_comment", func(t *testing.T) {
		a := surface.NewArtifact(context.Background(), nil, "dash-plan", theme.Dark(), theme.DefaultKeymap(), surface.WithReview())
		a.SetSize(60, 10)
		a.SetFocus(widget.FocusActive)
		a.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()})
		typeIntoArtifact(a, alphabet)
		if !strings.Contains(stripANSI(a.View()), alphabet) {
			t.Errorf("comment compose dropped printable keys; view:\n%s", stripANSI(a.View()))
		}
	})
}

// TestComposeDraftSurvivesBlur pins the focus-move guarantee behind ADR-0026's
// "panes hold their place": a half-typed compose line survives the pane losing
// and regaining focus. The host moves focus freely under the one-focused-pane
// model, so a blur must never clear the draft (the retired Esc handler used to).
// While blurred the draft is held, not rendered (the unfocused pane shows the
// focus hint), and the surface stops reporting CapturingText — so q quits from
// elsewhere; regaining focus shows the draft again, intact, and captures again.
func TestComposeDraftSurvivesBlur(t *testing.T) {
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", theme.Dark(), theme.DefaultKeymap(), surface.WithCompose())
	s.SetSize(60, 8)
	s.SetFocus(widget.FocusActive)
	typeInto(s, "draft in flight")
	if !s.CapturingText() {
		t.Fatal("precondition: a focused compose with a draft should capture")
	}

	s.SetFocus(widget.FocusSelected) // the host moved focus away
	if s.CapturingText() {
		t.Error("a blurred compose must not report capturing (q could never quit)")
	}

	s.SetFocus(widget.FocusActive) // the host moved focus back
	if !s.CapturingText() {
		t.Error("a refocused compose must capture again")
	}
	if !strings.Contains(stripANSI(s.View()), "draft in flight") {
		t.Errorf("compose draft lost across blur/refocus; view:\n%s", stripANSI(s.View()))
	}
}

// TestSurfaceSendIsOverridable proves the stream's send/confirm action is read
// from the keymap too: rebinding Enter drives the publish by the new key. With a
// nil client a real publish would panic, so the test asserts only that the
// composed line is consumed (the input clears) on the rebound key and not on the
// old default — enough to prove the binding, not the literal string, gates send.
func TestSurfaceSendIsOverridable(t *testing.T) {
	keys, err := theme.DefaultKeymap().Merge(
		theme.Override{Action: "Enter", Keys: []string{"ctrl+s"}},
	)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
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
