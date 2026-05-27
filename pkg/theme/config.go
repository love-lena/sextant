package theme

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the subset of `~/.config/sextant/config.toml` that the
// theme system reads. The TOML file is keyed by theme name and icon
// mode:
//
//	theme = "tomorrow-night"
//	icons = "nerd"     # or "ascii"
//
// Both fields are optional; absent values mean "use the built-in
// defaults" (DefaultTheme, IconModeNerd).
type Config struct {
	Theme string `toml:"theme"`
	Icons string `toml:"icons"`
}

// Resolved is the fully-resolved theme runtime: a Theme bundle, the
// icon set, and the icon mode that selected the set. Built by
// Resolve() and carried through the rest of the program.
type Resolved struct {
	Theme    Theme
	Icons    IconSet
	IconMode IconMode
}

// Overrides represent flag-layer or environment-layer values that
// outrank the config file. Empty strings mean "no override at this
// layer". Resolve() applies them per the precedence rule:
//
//	flag > SEXTANT_* env > config file > defaults
//
// Callers wire flags into FlagTheme / FlagIcons and pass env via
// EnvTheme / EnvIcons (which they read themselves so this package
// stays free of os.Getenv side-effects).
type Overrides struct {
	FlagTheme string
	FlagIcons string

	EnvTheme string
	EnvIcons string
}

// Resolve picks the theme name + icon mode per the precedence rule
// and loads the named theme from themesDir (a `<name>.yaml` file).
// When the resolved theme is empty or "default", the built-in
// adaptive DefaultTheme() is returned.
//
// themesDir may be empty; in that case theme loading is skipped and
// only the built-in default applies. This matters for tests and for
// the bootstrap path before `~/.config/sextant/themes/` exists.
func Resolve(cfg Config, ov Overrides, themesDir string) (Resolved, error) {
	name := pickString(ov.FlagTheme, ov.EnvTheme, cfg.Theme, "")
	mode, err := resolveIconMode(ov.FlagIcons, ov.EnvIcons, cfg.Icons)
	if err != nil {
		return Resolved{}, err
	}

	out := Resolved{
		Theme:    DefaultTheme(),
		Icons:    DefaultIcons(),
		IconMode: mode,
	}
	if name == "" || name == "default" {
		return out, nil
	}
	if themesDir == "" {
		return Resolved{}, fmt.Errorf("theme: theme=%q requested but no themes dir configured", name)
	}
	path := filepath.Join(themesDir, name+".yaml")
	t, err := LoadBase16(path)
	if err != nil {
		return Resolved{}, fmt.Errorf("theme: load %q: %w", name, err)
	}
	if t.Name == "" {
		t.Name = name
	}
	out.Theme = t
	return out, nil
}

// resolveIconMode applies the same precedence ladder for the icon
// mode field. Empty values at each layer pass through to the next.
func resolveIconMode(flag, env, file string) (IconMode, error) {
	pick := pickString(flag, env, file, "")
	mode, err := ParseIconMode(pick)
	if err != nil {
		return IconModeNerd, err
	}
	return mode, nil
}

// pickString returns the first non-empty value from layers in
// precedence order. Trailing fallback is returned if every layer is
// empty.
func pickString(layers ...string) string {
	for _, v := range layers {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// LoadConfig reads `~/.config/sextant/config.toml` (or the path
// given). Missing file is not an error — returns a zero Config. Parse
// errors propagate.
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled config path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("theme: read config %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("theme: parse config %s: %w", path, err)
	}
	return cfg, nil
}

// DefaultConfigPath returns `$HOME/.config/sextant/config.toml`.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("theme: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "sextant", "config.toml"), nil
}

// DefaultThemesDir returns `$HOME/.config/sextant/themes`.
func DefaultThemesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("theme: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "sextant", "themes"), nil
}
