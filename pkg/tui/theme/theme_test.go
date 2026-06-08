package theme_test

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

func TestLightDarkResolveDistinctBackgrounds(t *testing.T) {
	l := theme.Light()
	d := theme.Dark()
	if l.Variant != theme.VariantLight {
		t.Errorf("Light().Variant = %q, want %q", l.Variant, theme.VariantLight)
	}
	if d.Variant != theme.VariantDark {
		t.Errorf("Dark().Variant = %q, want %q", d.Variant, theme.VariantDark)
	}
	if l.Bg == d.Bg {
		t.Errorf("light and dark share a background colour %q", l.Bg)
	}
	// Pinned light value from the prototype.
	if got := l.Palette.Base00; got != "#fafafa" {
		t.Errorf("light Base00 = %q, want #fafafa (the pinned prototype value)", got)
	}
}

func TestNewUnknownVariantFallsBackToDark(t *testing.T) {
	got := theme.New(theme.Variant("nonsense"))
	if got.Variant != theme.VariantDark {
		t.Errorf("unknown variant resolved to %q, want dark", got.Variant)
	}
}

func TestRoleHueIsStablePerRole(t *testing.T) {
	d := theme.Dark()
	roles := []string{
		theme.RoleHuman, theme.RoleCoordinator, theme.RoleDispatcher,
		theme.RoleAgent, theme.RoleSystem,
	}
	seen := map[lipgloss.Color]string{}
	for _, r := range roles {
		c := d.RoleHue(r)
		if prev, dup := seen[c]; dup {
			t.Errorf("roles %q and %q share hue %q; one-hue-per-role broken", prev, r, c)
		}
		seen[c] = r
	}
	// Human is the accent (blue, base0D).
	if d.RoleHue(theme.RoleHuman) != d.Accent {
		t.Errorf("human role hue %q != accent %q", d.RoleHue(theme.RoleHuman), d.Accent)
	}
	// Unknown role falls back to default fg.
	if d.RoleHue("nope") != d.Fg {
		t.Errorf("unknown role hue = %q, want fg %q", d.RoleHue("nope"), d.Fg)
	}
}

func TestKindHueTintsSeparatelyFromRole(t *testing.T) {
	d := theme.Dark()
	// drain is red (base08), distinct from any role hue.
	if got, want := d.KindHue(theme.KindDrain), lipgloss.Color(d.Palette.Base08); got != want {
		t.Errorf("drain kind hue = %q, want %q", got, want)
	}
	if d.KindHue("unknown-kind") != lipgloss.Color(d.Palette.Base04) {
		t.Errorf("unknown kind hue = %q, want base04", d.KindHue("unknown-kind"))
	}
}

func TestStatusGlyphByShape(t *testing.T) {
	cases := map[theme.Status]string{
		theme.StatusConnected: "●",
		theme.StatusIdle:      "◔",
		theme.StatusDraining:  "⊘",
	}
	for st, want := range cases {
		if got := theme.StatusGlyph(st); got != want {
			t.Errorf("StatusGlyph(%q) = %q, want %q", st, got, want)
		}
	}
	if got := theme.StatusGlyph(theme.Status("unknown")); got != "○" {
		t.Errorf("unknown status glyph = %q, want ○", got)
	}
}

func TestDefaultKeymapBindsArrowsAndHJKL(t *testing.T) {
	km := theme.DefaultKeymap()
	if !key.Matches(keyMsg("up"), km.Up) || !key.Matches(keyMsg("k"), km.Up) {
		t.Error("Up should bind both up and k")
	}
	if !key.Matches(keyMsg("enter"), km.Enter) {
		t.Error("Enter should bind enter")
	}
	if !key.Matches(keyMsg("esc"), km.Back) {
		t.Error("Back should bind esc")
	}
	if !key.Matches(keyMsg("o"), km.Options) {
		t.Error("Options should bind o")
	}
	if !key.Matches(keyMsg("d"), km.DetailToggle) {
		t.Error("DetailToggle should bind d")
	}
	if !key.Matches(keyMsg("p"), km.PresetCycle) {
		t.Error("PresetCycle should bind p")
	}
	if !key.Matches(keyMsg("ctrl+c"), km.ForceQuit) {
		t.Error("ForceQuit should bind ctrl+c")
	}
}

func TestKeymapMergeOverridesLayoutShortcuts(t *testing.T) {
	base := theme.DefaultKeymap()
	merged := base.Merge(
		theme.Override{Action: "DetailToggle", Keys: []string{"ctrl+d"}},
		theme.Override{Action: "PresetCycle", Keys: []string{"tab"}},
	)
	if !key.Matches(keyMsg("ctrl+d"), merged.DetailToggle) {
		t.Error("merged DetailToggle should bind the override key ctrl+d")
	}
	if key.Matches(keyMsg("d"), merged.DetailToggle) {
		t.Error("merged DetailToggle should no longer bind d")
	}
	if !key.Matches(keyMsg("tab"), merged.PresetCycle) {
		t.Error("merged PresetCycle should bind the override key tab")
	}
	// The original is unchanged (Merge returns a copy).
	if !key.Matches(keyMsg("d"), base.DetailToggle) {
		t.Error("Merge mutated the receiver; original DetailToggle lost its d binding")
	}
	// Help text survives the override.
	if merged.DetailToggle.Help().Desc != base.DetailToggle.Help().Desc {
		t.Errorf("override changed help desc: %q vs %q", merged.DetailToggle.Help().Desc, base.DetailToggle.Help().Desc)
	}
}

func TestKeymapMergeOverridesAndPreservesOriginal(t *testing.T) {
	base := theme.DefaultKeymap()
	merged := base.Merge(theme.Override{Action: "Up", Keys: []string{"w"}})

	if !key.Matches(keyMsg("w"), merged.Up) {
		t.Error("merged Up should bind the override key w")
	}
	if key.Matches(keyMsg("k"), merged.Up) {
		t.Error("merged Up should no longer bind k")
	}
	// The original keymap is unchanged (Merge returns a copy).
	if !key.Matches(keyMsg("k"), base.Up) {
		t.Error("Merge mutated the receiver; original Up lost its k binding")
	}
	// Help text is preserved across an override.
	if merged.Up.Help().Desc != base.Up.Help().Desc {
		t.Errorf("override changed help desc: %q vs %q", merged.Up.Help().Desc, base.Up.Help().Desc)
	}
}

func TestKeymapMergeUnknownActionIsNoop(t *testing.T) {
	base := theme.DefaultKeymap()
	merged := base.Merge(theme.Override{Action: "Nonexistent", Keys: []string{"z"}})
	// Nothing should have changed.
	if !key.Matches(keyMsg("k"), merged.Up) {
		t.Error("unknown override should leave the keymap untouched")
	}
}
