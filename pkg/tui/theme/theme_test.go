package theme_test

import (
	"encoding/json"
	"strings"
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
	if !key.Matches(keyMsg("tab"), km.FocusNext) {
		t.Error("FocusNext should bind tab")
	}
	if !key.Matches(keyMsg("shift+tab"), km.FocusPrev) {
		t.Error("FocusPrev should bind shift+tab")
	}
	if !key.Matches(keyMsg("ctrl+h"), km.FocusLeft) || !key.Matches(keyMsg("ctrl+j"), km.FocusDown) ||
		!key.Matches(keyMsg("ctrl+k"), km.FocusUp) || !key.Matches(keyMsg("ctrl+l"), km.FocusRight) {
		t.Error("spatial focus should bind ctrl+h/j/k/l (left/down/up/right)")
	}
	if !key.Matches(keyMsg("o"), km.Options) {
		t.Error("Options should bind o")
	}
	if !key.Matches(keyMsg("p"), km.PresetCycle) {
		t.Error("PresetCycle should bind p")
	}
	if !key.Matches(keyMsg("ctrl+c"), km.ForceQuit) {
		t.Error("ForceQuit should bind ctrl+c")
	}
}

// TestKeymapMergeOverridesLayoutShortcuts proves a layout shortcut is an
// overridable default like any other binding: the override key acts, the old
// default does not, the receiver is unchanged, and the help text survives.
func TestKeymapMergeOverridesLayoutShortcuts(t *testing.T) {
	base := theme.DefaultKeymap()
	merged, err := base.Merge(
		theme.Override{Action: "PresetCycle", Keys: []string{"f2"}},
	)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !key.Matches(keyMsg("f2"), merged.PresetCycle) {
		t.Error("merged PresetCycle should bind the override key f2")
	}
	if key.Matches(keyMsg("p"), merged.PresetCycle) {
		t.Error("merged PresetCycle should no longer bind p")
	}
	// The original is unchanged (Merge returns a copy).
	if !key.Matches(keyMsg("p"), base.PresetCycle) {
		t.Error("Merge mutated the receiver; original PresetCycle lost its p binding")
	}
	// Help text survives the override.
	if merged.PresetCycle.Help().Desc != base.PresetCycle.Help().Desc {
		t.Errorf("override changed help desc: %q vs %q", merged.PresetCycle.Help().Desc, base.PresetCycle.Help().Desc)
	}
}

// TestKeymapMergeCollisionIsAnError pins the ambiguity guard: rebinding
// PresetCycle onto tab while FocusNext still holds tab would leave dispatch
// order to decide which action a tab press drives, silently — so Merge fails
// loud instead, naming the key and both actions.
func TestKeymapMergeCollisionIsAnError(t *testing.T) {
	_, err := theme.DefaultKeymap().Merge(
		theme.Override{Action: "PresetCycle", Keys: []string{"tab"}},
	)
	if err == nil {
		t.Fatal("binding PresetCycle to tab should collide with FocusNext's default tab")
	}
	for _, want := range []string{`"tab"`, "FocusNext", "PresetCycle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("collision error should name %s; got %q", want, err)
		}
	}
}

// TestKeymapMergeFreedKeyIsRebindable proves the collision check reads the
// MERGED state, not the defaults: a key freed by one override is available to
// another in the same Merge.
func TestKeymapMergeFreedKeyIsRebindable(t *testing.T) {
	merged, err := theme.DefaultKeymap().Merge(
		theme.Override{Action: "FocusNext", Keys: []string{"f3"}},
		theme.Override{Action: "PresetCycle", Keys: []string{"tab"}},
	)
	if err != nil {
		t.Fatalf("tab was freed by the FocusNext override; Merge should accept it: %v", err)
	}
	if !key.Matches(keyMsg("tab"), merged.PresetCycle) {
		t.Error("merged PresetCycle should bind the freed key tab")
	}
}

func TestKeymapMergeOverridesAndPreservesOriginal(t *testing.T) {
	base := theme.DefaultKeymap()
	merged, err := base.Merge(theme.Override{Action: "Up", Keys: []string{"w"}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

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
	merged, err := base.Merge(theme.Override{Action: "Nonexistent", Keys: []string{"z"}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// Nothing should have changed.
	if !key.Matches(keyMsg("k"), merged.Up) {
		t.Error("unknown override should leave the keymap untouched")
	}
}

// TestKeymapMergeNilKeysIsNoChange pins override semantics rule one: an
// override speaks only about the actions it names, so nil Keys (the zero
// value — a config entry that names an action but says nothing about keys)
// changes nothing. A typo'd omission can never silently stop an action.
func TestKeymapMergeNilKeysIsNoChange(t *testing.T) {
	merged, err := theme.DefaultKeymap().Merge(theme.Override{Action: "Options"})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !merged.Options.Enabled() {
		t.Error("nil Keys should not unbind the action")
	}
	if !key.Matches(keyMsg("o"), merged.Options) {
		t.Error("nil Keys should leave the action's default keys in place")
	}
}

// TestKeymapMergeEmptyKeysUnbinds pins rule two AT THE DECODE LEVEL: the
// distinction between "keys absent" (nil — no change) and "keys": [] (empty —
// explicit unbind) must survive JSON parsing, since that is how a config file
// will carry overrides. The overrides are unmarshalled, not hand-built, so the
// test proves the wire distinction, not just the Go-struct one.
func TestKeymapMergeEmptyKeysUnbinds(t *testing.T) {
	var overrides []theme.Override
	raw := `[{"action": "Options", "keys": []}, {"action": "Quit"}]`
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		t.Fatalf("unmarshal overrides: %v", err)
	}
	if overrides[0].Keys == nil {
		t.Fatal(`decode dropped the distinction: "keys": [] should decode to an empty non-nil slice`)
	}
	if overrides[1].Keys != nil {
		t.Fatal("decode invented keys: an absent field should decode to nil")
	}

	merged, err := theme.DefaultKeymap().Merge(overrides...)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	// "keys": [] is a deliberate unbind: the action is bound to nothing.
	if merged.Options.Enabled() {
		t.Error(`"keys": [] should unbind the action (Enabled() should report false)`)
	}
	if key.Matches(keyMsg("o"), merged.Options) {
		t.Error("an unbound action should not match its old default key")
	}
	// The absent-keys override left Quit alone.
	if !key.Matches(keyMsg("q"), merged.Quit) {
		t.Error("an absent keys field should leave the action's keys unchanged")
	}
}

// TestDefaultKeymapMergesClean pins the premise the collision check rests on:
// the default keymap binds every key string exactly once, so a bare Merge
// validates clean and any collision is introduced by an override.
func TestDefaultKeymapMergesClean(t *testing.T) {
	if _, err := theme.DefaultKeymap().Merge(); err != nil {
		t.Fatalf("the default keymap should be collision-free: %v", err)
	}
}
