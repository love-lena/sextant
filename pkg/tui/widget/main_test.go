package widget_test

import (
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain pins lipgloss to a TrueColor profile so golden renders carry the
// real hue ANSI deterministically, regardless of whether the test runs under a
// TTY, in CI, or piped. Without this, lipgloss would auto-detect the (absent)
// terminal and strip colour, making the focus/role hues invisible in the golden.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}
