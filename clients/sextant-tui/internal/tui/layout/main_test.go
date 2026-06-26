package layout_test

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain pins lipgloss to a TrueColor profile so golden renders carry the real
// hue ANSI deterministically, regardless of TTY/CI/pipe — the same pin the theme,
// widget, and surface packages use. Without it lipgloss would auto-detect the
// (absent) terminal and strip colour, making the focus/role hues invisible in the
// goldens.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
