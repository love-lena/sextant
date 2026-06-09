package widget

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// ComposeMaxRows is the row cap for the compose input: the compose grows from 1
// row up to this many as text wraps, then scrolls vertically within itself. Four
// rows gives the operator a full paragraph of context before the viewport kicks in
// — comfortable without dominating the pane.
const ComposeMaxRows = 4

// composePrompt is the prompt prefix rendered at the start of each row of the
// compose. It matches the hint text ">" used by the unfocused state so the width
// is consistent between the live input and the dim placeholder.
const composePrompt = "> "

// Compose is a wrapping, growing text-input for composing messages. It wraps
// text at the pane's inner width as the operator types, grows from 1 display row
// up to ComposeMaxRows, then scrolls vertically within itself once the cap is
// reached. Enter does NOT insert a newline — Enter is send, not break
// (ADR-0026; shift+enter for a literal newline is deferred).
//
// The widget tracks its current display height so the hosting surface can
// dynamically subtract it from the body viewport (body shrinks as compose grows).
// CapturingText semantics mirror the textinput convention: a focused Compose is
// capturing; a blurred one is not; the draft survives blur (pane holds its place).
type Compose struct {
	ta    textarea.Model
	width int
}

// NewCompose returns an empty Compose. Call SetWidth before View.
func NewCompose() Compose {
	ta := textarea.New()

	// Clean presentation: no line numbers, no thick border prompt, no end-of-buffer char.
	ta.ShowLineNumbers = false
	ta.EndOfBufferCharacter = 0
	ta.Prompt = composePrompt

	// Override styles: transparent base (no box/border), theme-neutral foreground.
	// The surface sets theme colours at View time; here we just strip the defaults.
	plain := lipgloss.NewStyle()
	ta.FocusedStyle = textarea.Style{
		Base:        plain,
		CursorLine:  plain,
		Text:        plain,
		Placeholder: plain,
		Prompt:      plain,
		EndOfBuffer: plain,
	}
	ta.BlurredStyle = textarea.Style{
		Base:        plain,
		CursorLine:  plain,
		Text:        plain,
		Placeholder: plain,
		Prompt:      plain,
		EndOfBuffer: plain,
	}

	// Disable the default Enter→InsertNewline key so Enter never inserts a newline.
	// Enter is send, not break (ADR-0026). The textarea still handles all other
	// editing bindings (Backspace, arrows, word-kill, etc.).
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys()) // no keys bound

	// Constrain. MaxHeight is set by SetWidth/relayout; height is 1 initially.
	ta.CharLimit = 0 // no limit
	ta.SetHeight(1)

	return Compose{ta: ta}
}

// SetWidth sets the compose width to w, recalculates the visual row count, and
// adjusts the textarea height to min(visualRows, ComposeMaxRows). Call this
// whenever the pane reflows.
func (c *Compose) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	c.width = w
	c.ta.SetWidth(w)
	c.resize()
}

// resize recomputes the visual row count of the current text and sets the
// textarea height to min(rows, ComposeMaxRows), so the compose grows as the
// operator types and caps at the configured maximum.
func (c *Compose) resize() {
	rows := c.visualRows()
	h := rows
	if h < 1 {
		h = 1
	}
	if h > ComposeMaxRows {
		h = ComposeMaxRows
	}
	c.ta.SetHeight(h)
}

// visualRows counts how many display rows the current text occupies at the
// current width. The textarea wraps at (width - promptWidth) characters. Each
// hard line is counted as its wrapped row count. An empty compose is 1 row.
func (c *Compose) visualRows() int {
	promptW := lipgloss.Width(composePrompt)
	bodyW := c.width - promptW
	if bodyW < 1 {
		bodyW = 1
	}
	text := c.ta.Value()
	if text == "" {
		return 1
	}
	total := 0
	for _, para := range strings.Split(text, "\n") {
		// Hard-break each paragraph to bodyW to count wrapped rows.
		rows := wrappedRowCount(para, bodyW)
		if rows < 1 {
			rows = 1
		}
		total += rows
	}
	if total < 1 {
		total = 1
	}
	return total
}

// wrappedRowCount returns how many rows a paragraph of rune-counted text takes
// when wrapped to width columns. An empty paragraph occupies 1 row (the cursor
// row). A paragraph with runeWidth cells takes ceil(runeWidth / width) rows,
// floored at 1.
func wrappedRowCount(para string, width int) int {
	if width < 1 {
		width = 1
	}
	runes := []rune(para)
	if len(runes) == 0 {
		return 1
	}
	// Count display cells (1 per BMP rune for simplicity; lipgloss handles East
	// Asian width but the compose wraps on characters, not cells).
	cells := lipgloss.Width(para)
	rows := (cells + width - 1) / width
	if rows < 1 {
		rows = 1
	}
	return rows
}

// Height returns the compose's current display height in rows: the visual row
// count of the current text, capped at ComposeMaxRows and floored at 1. The
// hosting surface subtracts this from the body height on each relayout.
func (c *Compose) Height() int {
	h := c.ta.Height()
	if h < 1 {
		h = 1
	}
	return h
}

// Value returns the current text content.
func (c *Compose) Value() string { return c.ta.Value() }

// SetValue sets the compose content programmatically (e.g. to clear it after
// send) and resizes.
func (c *Compose) SetValue(s string) {
	c.ta.SetValue(s)
	c.resize()
}

// Focus gives the compose input keyboard focus.
func (c *Compose) Focus() tea.Cmd {
	cmd := c.ta.Focus()
	return cmd
}

// Blur removes keyboard focus from the compose. The draft is retained — a blur
// must never clear the compose (ADR-0026: panes hold their place).
func (c *Compose) Blur() { c.ta.Blur() }

// Focused reports whether the compose is currently focused.
func (c *Compose) Focused() bool { return c.ta.Focused() }

// Update forwards a message to the textarea and resizes after the edit so
// Height() returns the current row count. Up/Down keys are NOT forwarded:
// up/down are the stream scroll bindings, so they are consumed by the surface
// before they reach the compose (the same routing textinput used).
func (c *Compose) Update(msg tea.Msg) (Compose, tea.Cmd) {
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	c.resize()
	return *c, cmd
}

// View renders the compose for the given focus state. When active the live
// textarea renders; when selected or idle a dim hint is shown (the operator sees
// their place, but the draft is not displayed — the typed text is held, not shown,
// while unfocused, consistent with the textinput convention). The rendered output
// is always exactly Height() rows wide.
func (c *Compose) View(t theme.Theme, focus Focus) string {
	if focus != FocusActive {
		// Unfocused placeholder: dim hint that focus-to-compose, padded to width.
		w := c.width
		if w <= 0 {
			w = 1
		}
		hint := "> focus pane to compose"
		return lipgloss.NewStyle().Foreground(t.Dim).Width(w).MaxWidth(w).Render(hint)
	}
	return c.ta.View()
}
