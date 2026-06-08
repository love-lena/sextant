package widget

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// Box draws a superfile/btop-style rounded panel of an exact outer w×h, with a
// coloured title chip spliced into the top border. The frame is built from plain
// runes and coloured per-segment — never by splicing ANSI into already-styled
// text — so the escape codes stay intact at any width. The border colour follows
// the focus state: dim when idle, accent when selected or active.
//
// The body is wrapped and padded to exactly the inner width and height (one
// column of horizontal breathing room), so a Box always occupies its full w×h
// regardless of body length.
func Box(t theme.Theme, focus Focus, title string, titleHue lipgloss.Color, body string, w, h int) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	bc := lipgloss.Color(focus.borderColor(t))
	innerW, innerH := w-2, h-2

	// Body: wrap + pad to exactly innerW×innerH, with one column of horizontal
	// padding inside the frame.
	content := lipgloss.NewStyle().
		Width(innerW).
		Height(innerH).
		MaxWidth(innerW).
		MaxHeight(innerH).
		Padding(0, 1).
		Foreground(t.Fg).
		Render(body)

	// Top border with the title chip: one lead dash, the bold-tinted chip, then
	// dashes filling to the corner.
	chip := " " + title + " "
	chipR := []rune(chip)
	dashes := innerW - 1 - len(chipR) // one lead dash before the chip
	if dashes < 0 {
		chipR = chipR[:max(0, innerW-1)]
		dashes = 0
	}
	seg := func(s string, c lipgloss.Color) string {
		return lipgloss.NewStyle().Foreground(c).Render(s)
	}
	segBold := func(s string, c lipgloss.Color) string {
		return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
	}

	top := seg("╭─", bc) + segBold(string(chipR), titleHue) + seg(strings.Repeat("─", dashes)+"╮", bc)
	bottom := seg("╰"+strings.Repeat("─", innerW)+"╯", bc)

	var b strings.Builder
	b.WriteString(top)
	b.WriteByte('\n')
	for _, cl := range strings.Split(content, "\n") {
		b.WriteString(seg("│", bc))
		b.WriteString(cl)
		b.WriteString(seg("│", bc))
		b.WriteByte('\n')
	}
	b.WriteString(bottom)
	return b.String()
}
