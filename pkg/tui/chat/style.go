package chat

import "github.com/charmbracelet/lipgloss"

// Styles is the package's role-token table. Every accent used anywhere
// in the chat TUI is named here and looked up by role — never inlined.
// This is what lets the visual treatment evolve without touching layout
// code in view.go. Spec §"Design system".
type Styles struct {
	// Surface-level accents.
	ActiveBorder   lipgloss.Style // focused surface border (composer in INSERT)
	Attention      lipgloss.Style // needs operator ack (e.g. pending permission badge)
	Destructive    lipgloss.Style // failed tool call, dangerous action
	Success        lipgloss.Style // ok tool call
	StreamPane     lipgloss.Style // rounded border around the stream area
	ComposerPane   lipgloss.Style // rounded border around the composer (NORMAL/parked tint)
	SelectedRow    lipgloss.Style // selected turn: lipgloss BorderLeft(▌) + bg tint + padding
	NonSelectedRow lipgloss.Style // non-selected turn: matching indent so columns align
	StatusBar      lipgloss.Style // bottom strip outside the panes
	KeyHintKey     lipgloss.Style // the key glyph in a status-bar hint ("j", "gg", "Esc")
	KeyHintDesc    lipgloss.Style // the descriptor next to the key ("step", "top·bot", "back")

	Muted        lipgloss.Style // de-emphasized text (timestamps, branch, hints)
	HeaderName   lipgloss.Style // agent name in header
	HeaderBranch lipgloss.Style // branch ref next to name

	ActorUser   lipgloss.Style // user turn glyph + name
	ActorAgent  lipgloss.Style // agent turn glyph + name
	ToolLine    lipgloss.Style // tool-call line under a turn
	HeaderRule  lipgloss.Style // thin rule under the header line
	TurnDivider lipgloss.Style // faint inter-turn rule

	StatusNormal lipgloss.Style // NORMAL mode pill (outlined)
	StatusInsert lipgloss.Style // INSERT mode pill (filled)
	StatusRead   lipgloss.Style // READ pill in --read mode

	ComposerActive      lipgloss.Style // composer when INSERT is active
	ComposerParked      lipgloss.Style // composer when NORMAL is active (dimmed)
	ComposerPaneFocused lipgloss.Style // composer box border when focus=FocusComposer
}

// defaultStyles returns the baseline role-token table. ANSI numeric
// colors keep the rendering portable across terminal palettes.
func defaultStyles() Styles {
	var (
		colAccent       = lipgloss.Color("4")  // blue
		colSelect       = lipgloss.Color("6")  // cyan (kept for ActorUser styling)
		colAttention    = lipgloss.Color("3")  // yellow
		colDestructive  = lipgloss.Color("1")  // red
		colSuccess      = lipgloss.Color("2")  // green
		colMuted        = lipgloss.Color("8")  // bright black
		colText         = lipgloss.Color("15") // bright white
		colBorderAccent = lipgloss.Color("12") // brighter blue for active composer (used in T6)
		// Borders: 244 is fine on dark; on light it's invisible. Adaptive gives
		// a visible mid-gray on both.
		colBorder = lipgloss.AdaptiveColor{Light: "247", Dark: "244"}
		// Selection background: 237 is near-black on light terminals. Adaptive
		// gives a subtle wash on both — pale gray on light, very dark gray on
		// dark.
		colSelectBg = lipgloss.AdaptiveColor{Light: "254", Dark: "237"}
		// Selection bar color: cyan reads OK on dark but dim on light; bump to
		// a brighter cyan on light, keep darker cyan on dark.
		colSelectBar = lipgloss.AdaptiveColor{Light: "27", Dark: "6"}
	)
	bold := lipgloss.NewStyle().Bold(true)
	return Styles{
		ActiveBorder: lipgloss.NewStyle().Foreground(colAccent),
		Attention:    bold.Foreground(colAttention),
		Destructive:  lipgloss.NewStyle().Foreground(colDestructive),
		Success:      lipgloss.NewStyle().Foreground(colSuccess),
		StreamPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 0),
		ComposerPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(0, 0),
		// SelectedRow combines:
		//   - left border using the ▌ glyph (drawn by lipgloss for every line)
		//   - subtle background tint that adapts to terminal theme
		// Content sits immediately right of the bar. Applied per-line in
		// view.go (see renderTurn for why per-line, not block).
		SelectedRow: lipgloss.NewStyle().
			Border(lipgloss.Border{Left: "▌"}, false, false, false, true).
			BorderForeground(colSelectBar).
			Background(colSelectBg).
			PaddingLeft(0),
		// NonSelectedRow keeps the same horizontal alignment as selected rows
		// (1 column where the bar would be) so columns line up across the
		// stream.
		NonSelectedRow: lipgloss.NewStyle().PaddingLeft(1),
		StatusBar:      lipgloss.NewStyle().Foreground(colMuted),
		KeyHintKey:     bold.Foreground(colAccent),
		KeyHintDesc:    lipgloss.NewStyle().Foreground(colMuted),
		Muted:          lipgloss.NewStyle().Foreground(colMuted),
		HeaderName:     bold.Foreground(colText),
		HeaderBranch:   lipgloss.NewStyle().Foreground(colMuted),
		ActorUser:      bold.Foreground(colSelect),
		ActorAgent:     bold.Foreground(colAccent),
		ToolLine:       lipgloss.NewStyle().Foreground(colMuted),
		HeaderRule:     lipgloss.NewStyle().Foreground(colBorder),
		TurnDivider:    lipgloss.NewStyle().Foreground(colBorder).Faint(true),
		StatusNormal:   bold.Foreground(colAccent),
		StatusInsert:   bold.Foreground(colText).Background(colAccent).Padding(0, 1),
		StatusRead:     bold.Foreground(colMuted),
		ComposerActive: lipgloss.NewStyle().Foreground(colText),
		ComposerParked: lipgloss.NewStyle().Foreground(colMuted),
		ComposerPaneFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorderAccent).
			Padding(0, 0),
	}
}
