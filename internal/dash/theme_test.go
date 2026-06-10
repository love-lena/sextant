package dash

import (
	"testing"

	"github.com/love-lena/sextant/pkg/tui/theme"
)

// TestThemeIntent pins the choice-vs-saved precedence that makes --theme auto
// a persistable, re-enterable value: an explicit flag choice this launch wins
// and is what gets persisted (an explicit auto resets a saved concrete theme
// back to detection); with no choice the saved one applies; and the result is
// always one of the three known variants, so a hand-edited config's garbage
// resolves to the default (auto) instead of round-tripping.
func TestThemeIntent(t *testing.T) {
	cases := []struct {
		name   string
		choice ThemeChoice
		saved  theme.Variant
		want   theme.Variant
	}{
		{"unset follows saved concrete", "", theme.VariantDark, theme.VariantDark},
		{"unset follows saved auto", "", theme.VariantAuto, theme.VariantAuto},
		{"unset + fresh config defaults to auto", "", "", theme.VariantAuto},
		{"unset + unknown saved value resolves to auto", "", "purple", theme.VariantAuto},
		{"explicit auto resets a saved concrete theme", ThemeAuto, theme.VariantDark, theme.VariantAuto},
		{"explicit light overrides saved auto", ThemeLight, theme.VariantAuto, theme.VariantLight},
		{"explicit dark overrides saved light", ThemeDark, theme.VariantLight, theme.VariantDark},
	}
	for _, tc := range cases {
		if got := themeIntent(tc.choice, tc.saved); got != tc.want {
			t.Errorf("%s: themeIntent(%q, %q) = %q, want %q", tc.name, tc.choice, tc.saved, got, tc.want)
		}
	}
}

// TestResolveThemeIsAlwaysConcrete: resolveTheme never hands the layout an
// unresolved variant — auto resolves to light or dark (by the bounded terminal
// probe, or its deterministic dark fallback off a tty), and the concrete
// choices map to themselves.
func TestResolveThemeIsAlwaysConcrete(t *testing.T) {
	if got := resolveTheme(theme.VariantLight).Variant; got != theme.VariantLight {
		t.Errorf("light resolved to %q", got)
	}
	if got := resolveTheme(theme.VariantDark).Variant; got != theme.VariantDark {
		t.Errorf("dark resolved to %q", got)
	}
	if got := resolveTheme(theme.VariantAuto).Variant; got != theme.VariantLight && got != theme.VariantDark {
		t.Errorf("auto resolved to %q, want a concrete variant", got)
	}
}
