package surface_test

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain pins lipgloss to a TrueColor profile so golden renders carry the real
// hue ANSI deterministically, regardless of TTY/CI/pipe — the same pin the widget
// package uses. glamour (used by the artifact surface) renders with its own
// pinned TrueColor profile and a fixed standard style, so its output is stable
// too.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
