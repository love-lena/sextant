// Package theme carries the dash's presentation opinions (ADR-0014): a base16
// palette, role/kind hue tokens, status-by-shape glyphs, and the locked
// keybinding set. It is the bottom of the dash TUI library — every widget and
// surface renders only from a Theme, never from raw colours or hardcoded keys.
//
// The presentation contract is deliberately narrow. A Theme resolves a base16
// scheme (light or dark, auto-detected by terminal background) into the handful
// of semantic styles a widget needs; RoleHue and KindHue map domain strings onto
// accent slots without the widget knowing what a "role" is; StatusGlyph reads
// liveness by shape, not colour. Keys live in a Keymap of named key.Bindings so
// nothing hardcodes a key — bindings are overridable defaults, not a contract.
package theme

import "github.com/charmbracelet/lipgloss"

// Variant names a stock base16 scheme. Auto resolves to one of the two concrete
// variants by detecting the terminal background; it is never itself a resolved
// Theme.
type Variant string

// The stock variants. Auto is the default: it detects the terminal background
// and falls back to dark when detection is unavailable.
const (
	VariantLight Variant = "light"
	VariantDark  Variant = "dark"
	VariantAuto  Variant = "auto"
)

// Palette is a base16 scheme: sixteen colours, base00–base0F. base00–base07 run
// background→foreground (the greyscale ramp); base08–base0F are the accent slots
// (red, orange, amber, green, teal, blue, purple, brown). Roles and kinds map
// onto the accent slots so the same scheme drives both the chrome and the hues.
type Palette struct {
	Base00 string // background
	Base01 string // panel / status-bar background
	Base02 string // faint fills, separators
	Base03 string // comments, dim text, borders
	Base04 string // muted foreground
	Base05 string // default foreground
	Base06 string // bright foreground
	Base07 string // titles (the brightest/darkest extreme)
	Base08 string // red    — alert / drain
	Base09 string // orange — dispatcher
	Base0A string // amber  — artifact
	Base0B string // green  — agent
	Base0C string // teal   — workflow
	Base0D string // blue   — human
	Base0E string // purple — coordinator
	Base0F string // brown
}

// lightPalette is the stock base16 "default" light scheme, verbatim from the
// dash-tui prototype's pinned light values (the accents are darkened stock
// base16 so role hues stay legible on a white background).
var lightPalette = Palette{
	Base00: "#fafafa",
	Base01: "#eeeeee",
	Base02: "#dcdcdc",
	Base03: "#a0a0a0",
	Base04: "#6c6c6c",
	Base05: "#2e2e2e",
	Base06: "#1c1c1c",
	Base07: "#111111",
	Base08: "#c0392b",
	Base09: "#b5651d",
	Base0A: "#b58900",
	Base0B: "#5c8a2f",
	Base0C: "#2d8a7e",
	Base0D: "#2b6cb0",
	Base0E: "#8e44ad",
	Base0F: "#8a5a30",
}

// darkPalette is the stock base16 "default" dark scheme (Chris Kempson's
// base16-default-dark), used verbatim with no hand-tuning.
var darkPalette = Palette{
	Base00: "#181818",
	Base01: "#282828",
	Base02: "#383838",
	Base03: "#585858",
	Base04: "#b8b8b8",
	Base05: "#d8d8d8",
	Base06: "#e8e8e8",
	Base07: "#f8f8f8",
	Base08: "#ab4642",
	Base09: "#dc9656",
	Base0A: "#f7ca88",
	Base0B: "#a1b56c",
	Base0C: "#86c1b9",
	Base0D: "#7cafc2",
	Base0E: "#ba8baf",
	Base0F: "#a16946",
}

// Theme is a resolved presentation context: the palette plus the derived
// semantic colours a widget renders from. A Theme is immutable once built; use
// the Light, Dark, or Auto constructors. Widgets take a Theme by value and read
// only its exported colours and helpers — they never reach for raw hex.
type Theme struct {
	// Variant records which concrete scheme this Theme resolved to (light or
	// dark), even when built through Auto.
	Variant Variant
	// Palette is the resolved base16 scheme.
	Palette Palette

	// Bg is the surface background (base00).
	Bg lipgloss.Color
	// Panel is the panel / status-bar background (base01).
	Panel lipgloss.Color
	// Fg is the default foreground (base05).
	Fg lipgloss.Color
	// Dim is muted/secondary text and the resting border colour (base03).
	Dim lipgloss.Color
	// Title is the brightest title foreground (base07).
	Title lipgloss.Color
	// Accent is the focus/selection hue — blue (base0D), the human role's slot.
	Accent lipgloss.Color
	// Line is the resting (idle) border colour (base03).
	Line lipgloss.Color
	// OnAccent is legible text painted on a saturated-accent background (base00).
	OnAccent lipgloss.Color
}

// New resolves a concrete (non-auto) variant into a Theme. An unknown variant
// resolves to dark.
func New(v Variant) Theme {
	var p Palette
	switch v {
	case VariantLight:
		p = lightPalette
	default:
		v = VariantDark
		p = darkPalette
	}
	return Theme{
		Variant:  v,
		Palette:  p,
		Bg:       lipgloss.Color(p.Base00),
		Panel:    lipgloss.Color(p.Base01),
		Fg:       lipgloss.Color(p.Base05),
		Dim:      lipgloss.Color(p.Base03),
		Title:    lipgloss.Color(p.Base07),
		Accent:   lipgloss.Color(p.Base0D),
		Line:     lipgloss.Color(p.Base03),
		OnAccent: lipgloss.Color(p.Base00),
	}
}

// Light returns the stock base16 light Theme.
func Light() Theme { return New(VariantLight) }

// Dark returns the stock base16 dark Theme.
func Dark() Theme { return New(VariantDark) }

// Auto returns the default Theme, choosing light or dark by detecting the
// terminal background; it falls back to dark when detection is unavailable (a
// non-terminal output, or a terminal that does not answer the background query).
// It is the dash's default; a --theme/config override selects Light or Dark
// explicitly.
func Auto() Theme {
	if detectLightBackground() {
		return Light()
	}
	return Dark()
}
