package widget

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
)

// maxViewportOffset is the largest top-line index that still shows the last
// line at the bottom of an h-row window, accounting for the top overflow cue
// renderViewport reserves once scrolled. When everything fits (numLines <= h)
// it is 0; otherwise the top cue eats one row, so the tail sits at
// numLines - (h-1). maxViewportOffset and renderViewport must agree: pinning to
// this offset shows the last line with no spurious bottom cue.
func maxViewportOffset(numLines, h int) int {
	if h <= 0 {
		return 0
	}
	if numLines <= h {
		return 0
	}
	// Scrolled (offset > 0) ⇒ a top cue is present, leaving h-1 content rows.
	budget := h - 1
	if budget < 1 {
		budget = 1
	}
	return max(0, numLines-budget)
}

// renderViewport renders a scrolling window of lines into exactly h rows, with
// ↑/↓ overflow cues that live within the height budget. It is the shared engine
// behind Stream and Detail: both append lines and scroll a window, and both want
// the same honest-cue behaviour. A cue is only drawn when content actually runs
// past that edge, and the rows it occupies are reserved up front so a ↓ cue is
// never the line that gets truncated away.
//
// offset is the index of the top visible line; h is the row budget; w is the
// render width. Lines are clamped to w (truncated, never wrapped) so the caller
// — typically Box — never has to re-wrap. Active focus brightens the cue colour.
func renderViewport(t theme.Theme, focus Focus, lines []string, offset, w, h int) string {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(lines) {
		offset = len(lines)
	}

	topCue := offset > 0
	bodyBudget := h
	if topCue {
		bodyBudget--
	}
	// A ↓ cue shows whenever the lines after the top cue still overrun the
	// remaining budget. Reserve its row from the budget once it will show.
	botCue := len(lines)-offset > bodyBudget
	if botCue {
		bodyBudget--
	}
	if bodyBudget < 0 {
		bodyBudget = 0
	}

	end := offset + bodyBudget
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[offset:end]

	cueHue := t.Dim
	if focus == FocusActive {
		cueHue = t.Accent
	}
	cue := func(text string) string {
		return lipgloss.NewStyle().Foreground(cueHue).Width(w).MaxWidth(w).Render(text)
	}

	var rows []string
	if topCue {
		rows = append(rows, cue("↑ more"))
	}
	for _, ln := range visible {
		rows = append(rows, lipgloss.NewStyle().Foreground(t.Fg).MaxWidth(w).Render(ln))
	}
	if botCue {
		rows = append(rows, cue("↓ more"))
	}
	return strings.Join(rows, "\n")
}
