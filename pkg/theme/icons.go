package theme

import "fmt"

// IconMode selects which glyph an Icon renders. Nerd Font is the
// default and the design target; ASCII is the fallback for terminals
// without Nerd Font installed.
//
// Per `conventions/tui-conventions.md` § "Icons" the ASCII layout
// must remain functionally usable — no missing information, no
// broken alignment — even when it looks worse.
type IconMode int

const (
	// IconModeNerd selects the Nerd Font glyph for each icon. Default.
	IconModeNerd IconMode = iota
	// IconModeASCII selects the ASCII fallback glyph.
	IconModeASCII
)

// String returns the config-file spelling ("nerd" / "ascii").
func (m IconMode) String() string {
	switch m {
	case IconModeNerd:
		return "nerd"
	case IconModeASCII:
		return "ascii"
	default:
		return fmt.Sprintf("icon-mode(%d)", int(m))
	}
}

// ParseIconMode parses the config-file spelling. Unknown values are
// rejected with an error so a typo in `config.toml` doesn't silently
// fall back.
func ParseIconMode(s string) (IconMode, error) {
	switch s {
	case "", "nerd":
		return IconModeNerd, nil
	case "ascii":
		return IconModeASCII, nil
	default:
		return IconModeNerd, fmt.Errorf("theme: unknown icon mode %q (want \"nerd\" or \"ascii\")", s)
	}
}

// Icon holds both glyphs for a single icon. Nerd is the design
// target; ASCII is the fallback. Selection happens via Icons.Get.
//
// Width matters: both fields should render as a single cell where
// possible so toggling modes doesn't shift content. Where Nerd glyphs
// are wider, the ASCII fallback should match the Nerd glyph's display
// width (pad with a space if needed).
type Icon struct {
	Nerd  string
	ASCII string
}

// Pick returns the glyph for the given mode.
func (i Icon) Pick(mode IconMode) string {
	if mode == IconModeASCII {
		return i.ASCII
	}
	return i.Nerd
}

// IconSet is the registry of every glyph sextant uses. Every icon is
// declared once, in one place, with both representations. Reaching
// for a new icon means adding both — the conventions doc
// (`§ "Icons"`) is explicit about this.
//
// Field names are chosen so callers read like the role they're
// surfacing (`icons.AgentRunning`, not `icons.GreenDot`).
type IconSet struct {
	// Status — agent lifecycle indicators.
	AgentRunning Icon
	AgentIdle    Icon
	AgentEnded   Icon
	AgentError   Icon

	// Decision queue states.
	DecisionPending Icon
	DecisionDone    Icon

	// General-purpose chrome.
	Selector  Icon // selection bar glyph (single cell)
	Search    Icon // magnifier for the search row
	Bullet    Icon // generic list bullet
	Check     Icon // success / completed
	Cross     Icon // failure / removed
	Warning   Icon // attention
	Info      Icon // informational accent
	Arrow     Icon // forward / next
	ChevronUp Icon
	ChevronDn Icon
	Spinner   Icon // canonical busy indicator (a single frame; spinner.go animates)
}

// DefaultIcons returns the canonical IconSet. The Nerd Font glyphs
// come from the Nerd Font 3 "octicons" / "material" packs that ship
// with every Nerd Font; the ASCII column avoids any non-7-bit ASCII.
//
// Padded with a trailing space where the Nerd glyph renders 1 cell
// and the ASCII spelling is 1 char — keeps the icon column at a
// consistent visible width.
func DefaultIcons() IconSet {
	return IconSet{
		AgentRunning: Icon{Nerd: "", ASCII: "*"}, //
		AgentIdle:    Icon{Nerd: "", ASCII: "."}, //
		AgentEnded:   Icon{Nerd: "", ASCII: "x"}, //
		AgentError:   Icon{Nerd: "", ASCII: "!"}, //

		DecisionPending: Icon{Nerd: "", ASCII: "?"},
		DecisionDone:    Icon{Nerd: "", ASCII: "+"},

		Selector:  Icon{Nerd: "▌", ASCII: ">"}, // ▌
		Search:    Icon{Nerd: "", ASCII: "/"}, //
		Bullet:    Icon{Nerd: "•", ASCII: "-"}, // •
		Check:     Icon{Nerd: "✓", ASCII: "+"}, // ✓
		Cross:     Icon{Nerd: "✗", ASCII: "x"}, // ✗
		Warning:   Icon{Nerd: "", ASCII: "!"},
		Info:      Icon{Nerd: "", ASCII: "i"},
		Arrow:     Icon{Nerd: "→", ASCII: "->"}, // →
		ChevronUp: Icon{Nerd: "▲", ASCII: "^"},  // ▲
		ChevronDn: Icon{Nerd: "▼", ASCII: "v"},  // ▼
		Spinner:   Icon{Nerd: "•", ASCII: "."},  // single-frame fallback; the bubbles spinner replaces this when animated.
	}
}
