package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	toolTreeGlyph = "└─"
	toolKindGlyph = "⚡"
)

// withSelBg returns the given style with the selection background
// applied, if `selected` is true; otherwise returns the style unchanged.
// Used by renderTurn and renderToolLine so every inner Render() emits
// `[fg+bg]text[reset]` and the selection tint stays continuous across
// the whole line — inner resets would otherwise clear the bg between
// chunks.
func (m *Model) withSelBg(s lipgloss.Style, selected bool) lipgloss.Style {
	if !selected {
		return s
	}
	return s.Background(m.styles.SelectedRow.GetBackground())
}

// selSpace returns n literal spaces, painted with the selection bg if
// the row is selected so plain-whitespace gaps between styled chunks
// don't render as untinted holes in the highlight.
func (m *Model) selSpace(selected bool, n int) string {
	pad := strings.Repeat(" ", n)
	if !selected {
		return pad
	}
	return lipgloss.NewStyle().Background(m.styles.SelectedRow.GetBackground()).Render(pad)
}

// View renders the component's content area: the stream pane and
// (in non-read mode) the composer pane. Header and status bar live
// on the standalone host — see `standalone.go` — so the dash can
// mount this same content rect without inheriting chrome it doesn't
// want.
//
// Width comes from the most recent SetSize call; everything below
// is width-aware so the boxes don't trail off the screen.
func (m *Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	stream := m.renderStreamBox(width)
	if m.opts.Read {
		return stream
	}
	composer := m.renderComposerBox(width)
	return strings.Join([]string{stream, composer}, "\n")
}

// renderHeader and renderStatusBar moved to standalone.go — they are
// host-owned chrome. The dash and other future hosts draw their own
// equivalents.

// renderStreamBox lays the stream content into a rounded-border pane.
// Inner width is total width minus the box's 2 border columns and 2
// padding columns. Position counter (e.g. "5/5") is overlaid on the
// bottom border per the spec.
func (m *Model) renderStreamBox(width int) string {
	// 2 border cols + 0 box-padding cols (Padding(0, 0) in StreamPane).
	inner := width - 2
	if inner < 20 {
		inner = 20
	}
	body := m.renderStream(inner)
	rendered := m.styles.StreamPane.Width(width - 2).Render(body)
	// Overlay "N/M" onto the bottom border. Lipgloss doesn't have a
	// first-class API for this, so we surgically replace a slice of
	// the last line. Skip if total turns is zero.
	if len(m.turns) == 0 {
		return rendered
	}
	posStr := fmt.Sprintf(" %d/%d ", m.selection+1, len(m.turns))
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}
	last := lines[len(lines)-1]
	if lipgloss.Width(last) >= lipgloss.Width(posStr)+6 {
		// Place the position string near the right edge of the bottom
		// border. Replace the corresponding rune slice.
		runes := []rune(stripANSI(last))
		insertAt := len(runes) - 5 - len([]rune(posStr))
		if insertAt > 4 {
			styled := m.styles.Muted.Render(posStr)
			// Surrounding border runes inherit the same muted/dim color
			// as the position string so the overlay reads as part of the
			// bottom border, not a contrasting label. Muted comes from
			// the role-token table (pkg/theme); never bind a bare ANSI
			// palette index here.
			before := m.styles.Muted.Render(string(runes[:insertAt]))
			after := m.styles.Muted.Render(string(runes[insertAt+len([]rune(posStr)):]))
			lines[len(lines)-1] = before + styled + after
			return strings.Join(lines, "\n")
		}
	}
	return rendered
}

// stripANSI strips ANSI escape sequences so we can do width math on
// the visible cells. Lipgloss provides ansi.Strip via x/ansi but we
// keep this local helper to avoid a wider import. Simple, not robust;
// only used for the bottom-border overlay above.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		if r == '\x1b' {
			in = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renderStream renders the turn list within an optional height budget.
// When the rendered output exceeds m.streamHeight rows, the output is
// clipped to a window centered on the selected turn (spec §"Selection-
// centered scroll"). The window is clamped at top and bottom so we never
// reference lines outside the rendered slice.
func (m *Model) renderStream(width int) string {
	// Render each turn and track its line range so we can find the
	// selected turn's position in the joined output later.
	type turnBlock struct {
		idx       int
		startLine int // line index in `allLines` where this turn starts
		endLine   int // exclusive
	}
	divLine := ""
	if len(m.turns) > 1 {
		divLine = m.styles.TurnDivider.Render(strings.Repeat(" ·", width/2))
	}

	var allLines []string
	var blocks []turnBlock
	for i, t := range m.turns {
		if i > 0 && divLine != "" {
			allLines = append(allLines, divLine)
		}
		start := len(allLines)
		rendered := m.renderTurn(i, t, width)
		turnLines := strings.Split(rendered, "\n")
		allLines = append(allLines, turnLines...)
		blocks = append(blocks, turnBlock{idx: i, startLine: start, endLine: len(allLines)})
	}

	// No height budget set (e.g. test that hasn't sent WindowSizeMsg).
	if m.streamHeight <= 0 {
		return strings.Join(allLines, "\n")
	}
	if len(allLines) <= m.streamHeight {
		// Content fits in the viewport. Top-pad with blank lines so the
		// stream hugs the bottom of the box (chat-app convention: oldest
		// content scrolls up, newest sits at the bottom edge). Without this
		// the rendered stream sits at the top of an over-large box, which
		// reads as "empty chat" rather than "few messages so far".
		pad := m.streamHeight - len(allLines)
		if pad > 0 {
			padded := make([]string, 0, m.streamHeight)
			for i := 0; i < pad; i++ {
				padded = append(padded, "")
			}
			padded = append(padded, allLines...)
			return strings.Join(padded, "\n")
		}
		return strings.Join(allLines, "\n")
	}

	// Find the selected turn's line range.
	var selStart, selEnd int
	for _, b := range blocks {
		if b.idx == m.selection {
			selStart, selEnd = b.startLine, b.endLine
			break
		}
	}
	// Center the window on the selected turn's midpoint line.
	selMid := (selStart + selEnd) / 2
	windowStart := selMid - m.streamHeight/2
	if windowStart < 0 {
		windowStart = 0
	}
	windowEnd := windowStart + m.streamHeight
	if windowEnd > len(allLines) {
		windowEnd = len(allLines)
		windowStart = windowEnd - m.streamHeight
		if windowStart < 0 {
			windowStart = 0
		}
	}
	return strings.Join(allLines[windowStart:windowEnd], "\n")
}

func (m *Model) renderTurn(idx int, t Turn, width int) string {
	selected := m.focus == FocusStream && idx == m.selection

	// Actor-specific name + style (used for name + colon). The icon
	// glyph was dropped — the bold-color name already conveys the actor
	// distinction.
	var (
		nameRaw  string
		actorSty lipgloss.Style
	)
	switch t.Actor {
	case ActorUser:
		nameRaw, actorSty = "you", m.styles.ActorUser
	case ActorAgent:
		nameRaw, actorSty = m.opts.AgentName, m.styles.ActorAgent
	case ActorSystem:
		nameRaw, actorSty = "system", m.styles.Muted
	default:
		nameRaw, actorSty = "?", m.styles.Muted
	}

	// Pre-styled chunks (bg-propagated when selected).
	actorStyled := m.withSelBg(actorSty, selected)
	mutedStyled := m.withSelBg(m.styles.Muted, selected)
	textStyled := m.withSelBg(lipgloss.NewStyle(), selected) // plain content w/ optional bg

	name := actorStyled.Render(nameRaw)
	colon := actorStyled.Render(":")
	tsStr := t.Ts.Format("15:04:05")
	ts := mutedStyled.Render(tsStr)

	// Visible widths used for layout math (un-styled char counts).
	// name + ":" + "   " = prefix visible width.
	prefixVis := lipgloss.Width(nameRaw) + 1 + 3
	tsVis := lipgloss.Width(tsStr)
	minGap := 2

	// Width available for the FIRST line of message text:
	//   rowInner - prefixVis - tsVis - minGap.
	// rowInner is `width - 1` because BorderLeft (selected) or
	// PaddingLeft=1 (non-selected) eats one col before content starts.
	rowInner := width - 1
	if rowInner < 10 {
		rowInner = 10
	}
	line1MsgWidth := rowInner - prefixVis - tsVis - minGap
	if line1MsgWidth < 10 {
		line1MsgWidth = 10
	}
	// Subsequent wrapped lines (and tool lines) take the full inner width.
	restWidth := rowInner

	// Wrap message text with a different width for the first line.
	msgChunks := wrapWithFirstWidth(t.Text, line1MsgWidth, restWidth)
	firstMsg := ""
	if len(msgChunks) > 0 {
		firstMsg = msgChunks[0]
	}

	// Compute the spacer that pushes the timestamp to the right edge of
	// line 1: pad so prefix + firstMsg + spacer + ts == rowInner exactly.
	spacer := rowInner - prefixVis - lipgloss.Width(firstMsg) - tsVis
	if spacer < minGap {
		spacer = minGap
	}

	// Build line 1.
	sp3 := m.selSpace(selected, 3)
	gap := m.selSpace(selected, spacer)
	line1 := name + colon + sp3 + textStyled.Render(firstMsg) + gap + ts

	// Subsequent wrapped lines and tool lines.
	var lines []string
	lines = append(lines, line1)
	for _, chunk := range msgChunks[1:] {
		lines = append(lines, textStyled.Render(chunk))
	}
	for _, tc := range t.ToolCalls {
		lines = append(lines, m.renderToolLine(tc, selected))
	}

	// Per-line SelectedRow / NonSelectedRow application: bar + bg + padding
	// for selected, plain left-pad for non-selected. Trailing pad on the
	// selected branch gets its own Background-only span so the tint
	// extends across whitespace to the right edge (lipgloss won't repaint
	// bg past inner `\x1b[0m` resets emitted by chunk renders).
	bgPad := lipgloss.NewStyle().Background(m.styles.SelectedRow.GetBackground())
	leftPadNonSel := strings.Repeat(" ", m.styles.NonSelectedRow.GetPaddingLeft())
	for i, line := range lines {
		visible := lipgloss.Width(line)
		pad := rowInner - visible
		if pad < 0 {
			pad = 0
		}
		if selected {
			padded := line + bgPad.Render(strings.Repeat(" ", pad))
			lines[i] = m.styles.SelectedRow.Render(padded)
		} else {
			lines[i] = leftPadNonSel + line + strings.Repeat(" ", pad)
		}
	}
	return strings.Join(lines, "\n")
}

// renderToolLine renders one tool call as a tree-connected sub-line.
// Format: "   └─ ⚡ <tool> (<arg>)    <status> · <duration>"
// Status word color routes through role tokens (Success/Destructive).
// Every chunk + literal space gets the selection bg propagated through
// withSelBg/selSpace so the highlight stays continuous when `selected`.
func (m *Model) renderToolLine(tc ToolCall, selected bool) string {
	statusStyle := m.withSelBg(m.styles.ToolLine, selected)
	statusWord := "pending"
	switch tc.Status {
	case ToolStatusOK:
		statusStyle = m.withSelBg(m.styles.Success, selected)
		statusWord = "ok"
	case ToolStatusFailed:
		statusStyle = m.withSelBg(m.styles.Destructive, selected)
		statusWord = "failed"
	}
	tool := m.withSelBg(m.styles.ToolLine, selected)
	muted := m.withSelBg(m.styles.Muted, selected)

	dur := ""
	if tc.Duration > 0 {
		dur = m.selSpace(selected, 1) + muted.Render("· "+tc.Duration.Truncate(1e6).String())
	}
	arg := ""
	if tc.Arg != "" {
		arg = m.selSpace(selected, 1) + tool.Render("("+tc.Arg+")")
	}
	return tool.Render(toolTreeGlyph) +
		m.selSpace(selected, 1) +
		tool.Render(toolKindGlyph) +
		m.selSpace(selected, 1) +
		tool.Render(tc.Name) +
		arg +
		m.selSpace(selected, 4) +
		statusStyle.Render(statusWord) +
		dur
}

// wrapWithFirstWidth wraps text into lines whose visible widths fit
// within firstWidth (line 0) and restWidth (subsequent lines). Lines
// that already fit are emitted verbatim — internal whitespace,
// leading indents, and ASCII-art alignment are preserved. Only lines
// that exceed their budget are word-wrapped (which does collapse
// consecutive whitespace within the wrapped output — acceptable
// trade-off for prose paragraphs, which are the only place wrap ever
// fires in practice).
//
// Each input paragraph (split on `\n`) is independent: the first
// paragraph's first line uses firstWidth, every other line of every
// paragraph uses restWidth.
func wrapWithFirstWidth(text string, firstWidth, restWidth int) []string {
	if text == "" {
		return []string{""}
	}
	var out []string
	first := true
	for _, paragraph := range strings.Split(text, "\n") {
		budget := restWidth
		if first {
			budget = firstWidth
		}
		first = false

		if paragraph == "" {
			out = append(out, "")
			continue
		}

		// Fast path: the whole paragraph fits as-is. Preserve verbatim,
		// including all internal whitespace.
		if lipgloss.Width(paragraph) <= budget {
			out = append(out, paragraph)
			continue
		}

		// Slow path: paragraph exceeds the budget. Word-wrap word-by-word.
		// This is the path that does collapse internal whitespace — only
		// fires for over-budget paragraphs (long prose).
		out = append(out, wordWrap(paragraph, budget, restWidth)...)
	}
	return out
}

// wordWrap breaks a single paragraph into lines whose visible widths
// fit within firstWidth (line 0) and restWidth (subsequent lines).
// Internal whitespace runs are collapsed to single spaces — this is the
// trade-off for the slow-path wrap. Most chat content takes the fast
// path in wrapWithFirstWidth and avoids this entirely.
func wordWrap(text string, firstWidth, restWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	budget := firstWidth
	var line strings.Builder
	for _, w := range words {
		if line.Len() == 0 {
			line.WriteString(w)
			continue
		}
		if lipgloss.Width(line.String())+1+lipgloss.Width(w) > budget {
			out = append(out, line.String())
			line.Reset()
			line.WriteString(w)
			budget = restWidth
			continue
		}
		line.WriteString(" ")
		line.WriteString(w)
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	return out
}

// renderComposerBox places the composer inside a rounded pane. Border
// color indicates focus:
//   - FocusComposer (NORMAL or INSERT): accent color — signals that 'i'
//     will land here.
//   - FocusStream (or Read mode): muted border — parked/inactive.
func (m *Model) renderComposerBox(width int) string {
	// SetWidth so the textarea wraps to the available inner width.
	// Width-2 accounts for the rounded box border on left+right.
	m.composer.SetWidth(width - 2)
	pane := m.styles.ComposerPane
	if m.focus == FocusComposer {
		pane = m.styles.ComposerPaneFocused
	}
	return pane.Width(width - 2).Render(m.composer.View())
}

// renderStatusBar moved to standalone.go — see Standalone.View().
