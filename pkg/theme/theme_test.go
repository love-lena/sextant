package theme

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestLoadBase16TomorrowNightSlotMapping is the golden test for
// slot→role mapping. The reference scheme is tomorrow-night; the
// expected role values are derived from the documented mapping in
// `base16.go` § ToTheme. If the mapping changes, this test must
// change with it — and the diff is reviewable.
func TestLoadBase16TomorrowNightSlotMapping(t *testing.T) {
	t.Parallel()
	path := filepath.Join("testdata", "tomorrow-night.yaml")
	got, err := LoadBase16(path)
	if err != nil {
		t.Fatalf("LoadBase16: %v", err)
	}

	want := map[string]lipgloss.TerminalColor{
		"Background":      lipgloss.Color("#1d1f21"), // base00
		"BackgroundAlt":   lipgloss.Color("#282a2e"), // base01
		"Foreground":      lipgloss.Color("#c5c8c6"), // base05
		"ForegroundMuted": lipgloss.Color("#969896"), // base03
		"Border":          lipgloss.Color("#969896"), // base03
		"BorderActive":    lipgloss.Color("#81a2be"), // base0D
		"Accent":          lipgloss.Color("#81a2be"), // base0D
		"Danger":          lipgloss.Color("#cc6666"), // base08
		"Warning":         lipgloss.Color("#de935f"), // base09
		"Success":         lipgloss.Color("#b5bd68"), // base0B
	}
	checks := map[string]lipgloss.TerminalColor{
		"Background":      got.Background,
		"BackgroundAlt":   got.BackgroundAlt,
		"Foreground":      got.Foreground,
		"ForegroundMuted": got.ForegroundMuted,
		"Border":          got.Border,
		"BorderActive":    got.BorderActive,
		"Accent":          got.Accent,
		"Danger":          got.Danger,
		"Warning":         got.Warning,
		"Success":         got.Success,
	}
	for role, v := range checks {
		w := want[role]
		if v != w {
			t.Errorf("role %s: got %#v, want %#v", role, v, w)
		}
	}
	if got.Name != "Tomorrow Night" {
		t.Errorf("Name: got %q, want %q", got.Name, "Tomorrow Night")
	}
}

// TestLoadBase16AcceptsLeadingHash exercises the defensive trim on
// `#`. Some base16 schemes in the wild include it; the loader must
// accept both spellings.
func TestLoadBase16AcceptsLeadingHash(t *testing.T) {
	t.Parallel()
	raw := []byte(`
scheme: "withhash"
base00: "#1d1f21"
base01: "282a2e"
base02: "373b41"
base03: "969896"
base04: "b4b7b4"
base05: "c5c8c6"
base06: "e0e0e0"
base07: "ffffff"
base08: "cc6666"
base09: "de935f"
base0A: "f0c674"
base0B: "b5bd68"
base0C: "8abeb7"
base0D: "81a2be"
base0E: "b294bb"
base0F: "a3685a"
`)
	got, err := ParseBase16(raw)
	if err != nil {
		t.Fatalf("ParseBase16: %v", err)
	}
	if got.Background != lipgloss.Color("#1d1f21") {
		t.Errorf("Background: leading # not stripped, got %#v", got.Background)
	}
}

// TestLoadBase16RejectsMissingSlots ensures incomplete schemes fail
// loudly instead of silently rendering with empty color values
// (which would look like "unstyled text" — confusing to debug).
func TestLoadBase16RejectsMissingSlots(t *testing.T) {
	t.Parallel()
	raw := []byte(`
scheme: "incomplete"
base00: "1d1f21"
`)
	if _, err := ParseBase16(raw); err == nil {
		t.Fatal("ParseBase16: expected error for incomplete scheme, got nil")
	}
}

// TestLoadBase16RejectsBadHex catches typos / non-hex values before
// they reach Lipgloss (which would render them as no-color).
func TestLoadBase16RejectsBadHex(t *testing.T) {
	t.Parallel()
	raw := []byte(`
scheme: "badhex"
base00: "not-hex"
base01: "282a2e"
base02: "373b41"
base03: "969896"
base04: "b4b7b4"
base05: "c5c8c6"
base06: "e0e0e0"
base07: "ffffff"
base08: "cc6666"
base09: "de935f"
base0A: "f0c674"
base0B: "b5bd68"
base0C: "8abeb7"
base0D: "81a2be"
base0E: "b294bb"
base0F: "a3685a"
`)
	if _, err := ParseBase16(raw); err == nil {
		t.Fatal("ParseBase16: expected error for non-hex value, got nil")
	}
}

// TestDefaultThemePopulatesEveryRole pins the built-in adaptive
// theme. Every role field must be non-nil and Empty() must report
// false — operators rely on this when no theme file is present.
func TestDefaultThemePopulatesEveryRole(t *testing.T) {
	t.Parallel()
	th := DefaultTheme()
	if th.Empty() {
		t.Fatal("DefaultTheme: reports Empty()")
	}
	checks := map[string]lipgloss.TerminalColor{
		"Background":      th.Background,
		"BackgroundAlt":   th.BackgroundAlt,
		"Foreground":      th.Foreground,
		"ForegroundMuted": th.ForegroundMuted,
		"Border":          th.Border,
		"BorderActive":    th.BorderActive,
		"Accent":          th.Accent,
		"Danger":          th.Danger,
		"Warning":         th.Warning,
		"Success":         th.Success,
	}
	for name, v := range checks {
		if v == nil {
			t.Errorf("DefaultTheme: role %s is nil", name)
			continue
		}
		// All defaults must be AdaptiveColor — bare colors break on
		// the opposite background per `conventions/tui-conventions.md`
		// § "Adaptive colors are the default".
		if _, ok := v.(lipgloss.AdaptiveColor); !ok {
			t.Errorf("DefaultTheme: role %s is %T, want lipgloss.AdaptiveColor", name, v)
		}
	}
	if th.Name != "default" {
		t.Errorf("Name: got %q, want %q", th.Name, "default")
	}
}

// TestDefaultThemeRendersNonEmpty checks every role rendered against
// the default theme produces a non-empty styled string — i.e. the
// role actually carries a color value Lipgloss can lay onto text.
func TestDefaultThemeRendersNonEmpty(t *testing.T) {
	t.Parallel()
	th := DefaultTheme()
	cases := map[string]lipgloss.TerminalColor{
		"Background":      th.Background,
		"BackgroundAlt":   th.BackgroundAlt,
		"Foreground":      th.Foreground,
		"ForegroundMuted": th.ForegroundMuted,
		"Border":          th.Border,
		"BorderActive":    th.BorderActive,
		"Accent":          th.Accent,
		"Danger":          th.Danger,
		"Warning":         th.Warning,
		"Success":         th.Success,
	}
	for name, c := range cases {
		out := lipgloss.NewStyle().Foreground(c).Render("x")
		if out == "" {
			t.Errorf("role %s: rendered empty string", name)
		}
	}
}

// TestIconFallbackPicksASCIIInASCIIMode verifies the IconMode toggle
// flips every glyph in the registry from Nerd to ASCII. This is the
// load-bearing operator-experience claim — `config.icons = "ascii"`
// must not silently keep the Nerd glyph for any icon.
func TestIconFallbackPicksASCIIInASCIIMode(t *testing.T) {
	t.Parallel()
	icons := DefaultIcons()
	// Pull every Icon field via reflection-light pattern: enumerate a
	// known-good slice rather than reaching into the struct. Adding a
	// new icon to DefaultIcons() also requires adding it here, which
	// is the right level of pressure — the convention is "every icon
	// has both representations declared".
	all := []struct {
		name string
		ic   Icon
	}{
		{"AgentRunning", icons.AgentRunning},
		{"AgentIdle", icons.AgentIdle},
		{"AgentEnded", icons.AgentEnded},
		{"AgentError", icons.AgentError},
		{"DecisionPending", icons.DecisionPending},
		{"DecisionDone", icons.DecisionDone},
		{"Selector", icons.Selector},
		{"Search", icons.Search},
		{"Bullet", icons.Bullet},
		{"Check", icons.Check},
		{"Cross", icons.Cross},
		{"Warning", icons.Warning},
		{"Info", icons.Info},
		{"Arrow", icons.Arrow},
		{"ChevronUp", icons.ChevronUp},
		{"ChevronDn", icons.ChevronDn},
		{"Spinner", icons.Spinner},
	}
	for _, c := range all {
		if c.ic.Nerd == "" {
			t.Errorf("icon %s: Nerd glyph missing", c.name)
		}
		if c.ic.ASCII == "" {
			t.Errorf("icon %s: ASCII glyph missing", c.name)
		}
		if got := c.ic.Pick(IconModeNerd); got != c.ic.Nerd {
			t.Errorf("icon %s: Pick(Nerd) = %q, want %q", c.name, got, c.ic.Nerd)
		}
		if got := c.ic.Pick(IconModeASCII); got != c.ic.ASCII {
			t.Errorf("icon %s: Pick(ASCII) = %q, want %q", c.name, got, c.ic.ASCII)
		}
	}
}

// TestParseIconMode covers the config-file string spellings + the
// rejection path for typos.
func TestParseIconMode(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		want    IconMode
		wantErr bool
	}{
		"":      {want: IconModeNerd},
		"nerd":  {want: IconModeNerd},
		"ascii": {want: IconModeASCII},
		"NERD":  {wantErr: true}, // case-sensitive — toml strings are exact
		"glyph": {wantErr: true},
	}
	for in, want := range cases {
		got, err := ParseIconMode(in)
		if (err != nil) != want.wantErr {
			t.Errorf("ParseIconMode(%q): err=%v, wantErr=%v", in, err, want.wantErr)
			continue
		}
		if !want.wantErr && got != want.want {
			t.Errorf("ParseIconMode(%q): got %v, want %v", in, got, want.want)
		}
	}
}

// TestResolvePrecedence pins the flag > env > file > defaults
// precedence rule. Each layer overrides the layers below it.
func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "filey.yaml"), validBase16("filey"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "envy.yaml"), validBase16("envy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flagy.yaml"), validBase16("flagy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Flag wins over env and file.
	r, err := Resolve(
		Config{Theme: "filey", Icons: "ascii"},
		Overrides{FlagTheme: "flagy", EnvTheme: "envy", FlagIcons: "nerd"},
		dir,
	)
	if err != nil {
		t.Fatalf("Resolve flag-precedence: %v", err)
	}
	if r.Theme.Name != "flagy" {
		t.Errorf("flag should win: got theme %q, want %q", r.Theme.Name, "flagy")
	}
	if r.IconMode != IconModeNerd {
		t.Errorf("flag icon should win: got %v, want %v", r.IconMode, IconModeNerd)
	}

	// Env wins over file when flag is empty.
	r, err = Resolve(
		Config{Theme: "filey", Icons: "ascii"},
		Overrides{EnvTheme: "envy"},
		dir,
	)
	if err != nil {
		t.Fatalf("Resolve env-precedence: %v", err)
	}
	if r.Theme.Name != "envy" {
		t.Errorf("env should win: got theme %q, want %q", r.Theme.Name, "envy")
	}
	if r.IconMode != IconModeASCII {
		t.Errorf("file icon should win: got %v, want %v", r.IconMode, IconModeASCII)
	}

	// File wins when no overrides.
	r, err = Resolve(
		Config{Theme: "filey", Icons: "ascii"},
		Overrides{},
		dir,
	)
	if err != nil {
		t.Fatalf("Resolve file-precedence: %v", err)
	}
	if r.Theme.Name != "filey" {
		t.Errorf("file should win: got theme %q, want %q", r.Theme.Name, "filey")
	}

	// All empty → default theme.
	r, err = Resolve(Config{}, Overrides{}, dir)
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	if r.Theme.Name != "default" {
		t.Errorf("default should win: got theme %q, want %q", r.Theme.Name, "default")
	}
	if r.IconMode != IconModeNerd {
		t.Errorf("default icon: got %v, want %v", r.IconMode, IconModeNerd)
	}
}

// TestLoadConfigMissingFileReturnsZero exercises the "no config
// file" path — sextant must boot cleanly before the operator has
// written `config.toml`.
func TestLoadConfigMissingFileReturnsZero(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("LoadConfig missing: %v", err)
	}
	if cfg.Theme != "" || cfg.Icons != "" {
		t.Errorf("LoadConfig: zero config expected, got %+v", cfg)
	}
}

// TestLoadConfigParsesKeys covers the documented schema:
// `theme = "<name>"` and `icons = "nerd" | "ascii"`.
func TestLoadConfigParsesKeys(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`theme = "tomorrow-night"
icons = "ascii"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Theme != "tomorrow-night" {
		t.Errorf("Theme: got %q, want %q", cfg.Theme, "tomorrow-night")
	}
	if cfg.Icons != "ascii" {
		t.Errorf("Icons: got %q, want %q", cfg.Icons, "ascii")
	}
}

// validBase16 produces a minimal valid base16 YAML body for tests
// that need to stub a theme file on disk.
func validBase16(scheme string) []byte {
	return []byte(`scheme: "` + scheme + `"
base00: "1d1f21"
base01: "282a2e"
base02: "373b41"
base03: "969896"
base04: "b4b7b4"
base05: "c5c8c6"
base06: "e0e0e0"
base07: "ffffff"
base08: "cc6666"
base09: "de935f"
base0A: "f0c674"
base0B: "b5bd68"
base0C: "8abeb7"
base0D: "81a2be"
base0E: "b294bb"
base0F: "a3685a"
`)
}
