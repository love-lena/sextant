package chat

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/love-lena/sextant/pkg/theme"
)

// Styles is the package's role-token table. Every accent used anywhere
// in the chat TUI is named here and looked up by role — never inlined.
// This is what lets the visual treatment evolve without touching layout
// code in view.go. Spec §"Design system".
//
// Concrete `lipgloss.Color` values do not live here. The table is
// hydrated from `pkg/theme.Theme` via stylesFor, which is the only
// site this package consults the theme. Adding a new role token here
// usually means binding it in stylesFor too.
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

	// Lost is the distinct bright-red dot for the "lost" lifecycle state.
	// Separate from Destructive so the two can diverge visually if needed.
	Lost  lipgloss.Style // agent lost — bright red; distinct from Destructive
	Muted lipgloss.Style // de-emphasized text (timestamps, branch, hints)
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

// defaultStyles returns the baseline role-token table, hydrated from
// `pkg/theme`'s built-in adaptive theme. Tests and previews use this
// constructor; callers that load a base16 theme can call StylesFor
// with their own Theme.
func defaultStyles() Styles { return StylesFor(theme.DefaultTheme()) }

// StylesFor binds a role-token table to a concrete theme. The mapping
// from theme roles → chat-specific style fields is documented inline.
//
// "ActorUser" picks up a secondary cyan tone in the default theme but
// no role token is dedicated to it; we bind it to BorderActive (the
// next-most-prominent signal) so a base16 theme without a separate
// "user" hue still renders distinguishable. Adjust if a theme wants
// finer control — that's the pressure point that would justify
// promoting a sixth signal role.
func StylesFor(th theme.Theme) Styles {
	bold := lipgloss.NewStyle().Bold(true)
	// SelectedRow tint: we want a *subtle* background wash, not the
	// theme's full BackgroundAlt panel. The theme exposes BackgroundAlt
	// for exactly this purpose — base16 schemes pin base01 (the
	// alternate background) as the selection tint slot.
	colSelectBg := th.BackgroundAlt
	colSelectBar := th.Accent
	return Styles{
		ActiveBorder: lipgloss.NewStyle().Foreground(th.Accent),
		Attention:    bold.Foreground(th.Warning),
		Destructive:  lipgloss.NewStyle().Foreground(th.Danger),
		Lost:         lipgloss.NewStyle().Foreground(lipgloss.Color("9")), // bright red; ANSI color 9
		Success:      lipgloss.NewStyle().Foreground(th.Success),
		StreamPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(th.Border).
			Padding(0, 0),
		ComposerPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(th.Border).
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
		NonSelectedRow:      lipgloss.NewStyle().PaddingLeft(1),
		StatusBar:           lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		KeyHintKey:          bold.Foreground(th.Accent),
		KeyHintDesc:         lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		Muted:               lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		HeaderName:          bold.Foreground(th.Foreground),
		HeaderBranch:        lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		ActorUser:           bold.Foreground(th.BorderActive),
		ActorAgent:          bold.Foreground(th.Accent),
		ToolLine:            lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		HeaderRule:          lipgloss.NewStyle().Foreground(th.Border),
		TurnDivider:         lipgloss.NewStyle().Foreground(th.Border).Faint(true),
		StatusNormal:        bold.Foreground(th.Accent),
		StatusInsert:        bold.Foreground(th.Foreground).Background(th.Accent).Padding(0, 1),
		StatusRead:          bold.Foreground(th.ForegroundMuted),
		ComposerActive:      lipgloss.NewStyle().Foreground(th.Foreground),
		ComposerParked:      lipgloss.NewStyle().Foreground(th.ForegroundMuted),
		ComposerPaneFocused: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(th.BorderActive).Padding(0, 0),
	}
}
