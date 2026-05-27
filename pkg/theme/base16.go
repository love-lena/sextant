package theme

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// Base16Scheme is the on-disk YAML shape. base16 schemes ship as YAML
// with sixteen palette slots (`base00`–`base0F`) plus arbitrary
// scheme/author metadata. The slot vocabulary is fixed:
//
//	base00  default background
//	base01  lighter background (status bars, line numbers)
//	base02  selection background
//	base03  comments / invisibles / muted text
//	base04  dark foreground (status bars)
//	base05  default foreground
//	base06  light foreground
//	base07  light background / "white"
//	base08  red       (variables, errors)
//	base09  orange    (integers, links)
//	base0A  yellow    (classes, search)
//	base0B  green     (strings, success)
//	base0C  cyan      (regex, escapes)
//	base0D  blue      (functions, focus)
//	base0E  magenta   (keywords)
//	base0F  brown     (deprecated)
//
// Each value is a 6-digit hex string without the leading `#`. This is
// the canonical base16 schema; we accept a leading `#` defensively
// because some scheme files in the wild include one.
type Base16Scheme struct {
	Scheme string `yaml:"scheme"`
	Author string `yaml:"author"`

	Base00 string `yaml:"base00"`
	Base01 string `yaml:"base01"`
	Base02 string `yaml:"base02"`
	Base03 string `yaml:"base03"`
	Base04 string `yaml:"base04"`
	Base05 string `yaml:"base05"`
	Base06 string `yaml:"base06"`
	Base07 string `yaml:"base07"`
	Base08 string `yaml:"base08"`
	Base09 string `yaml:"base09"`
	Base0A string `yaml:"base0A"`
	Base0B string `yaml:"base0B"`
	Base0C string `yaml:"base0C"`
	Base0D string `yaml:"base0D"`
	Base0E string `yaml:"base0E"`
	Base0F string `yaml:"base0F"`
}

// LoadBase16 reads a base16 YAML file from path, validates the slot
// shape, and returns a Theme whose role tokens are populated per the
// default slot→role mapping (see ToTheme).
func LoadBase16(path string) (Theme, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied theme path
	if err != nil {
		return Theme{}, fmt.Errorf("theme: read %s: %w", path, err)
	}
	return ParseBase16(raw)
}

// ParseBase16 parses base16 YAML from raw bytes and returns the
// resulting Theme. Separated from LoadBase16 so tests can drive the
// loader without touching disk.
func ParseBase16(raw []byte) (Theme, error) {
	var s Base16Scheme
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return Theme{}, fmt.Errorf("theme: parse base16 yaml: %w", err)
	}
	if err := s.validate(); err != nil {
		return Theme{}, err
	}
	return s.ToTheme(), nil
}

// validate asserts every required slot is present and looks hex-shaped.
// We accept lower-case, upper-case, and an optional leading `#`.
func (s Base16Scheme) validate() error {
	slots := []struct {
		name  string
		value string
	}{
		{"base00", s.Base00},
		{"base01", s.Base01},
		{"base02", s.Base02},
		{"base03", s.Base03},
		{"base04", s.Base04},
		{"base05", s.Base05},
		{"base06", s.Base06},
		{"base07", s.Base07},
		{"base08", s.Base08},
		{"base09", s.Base09},
		{"base0A", s.Base0A},
		{"base0B", s.Base0B},
		{"base0C", s.Base0C},
		{"base0D", s.Base0D},
		{"base0E", s.Base0E},
		{"base0F", s.Base0F},
	}
	for _, slot := range slots {
		v := strings.TrimSpace(slot.value)
		if v == "" {
			return fmt.Errorf("theme: base16 slot %s missing", slot.name)
		}
		if !looksHex(v) {
			return fmt.Errorf("theme: base16 slot %s not hex-shaped: %q", slot.name, slot.value)
		}
	}
	return nil
}

// looksHex returns true when v is 6 hex digits with an optional `#`.
func looksHex(v string) bool {
	v = strings.TrimPrefix(v, "#")
	if len(v) != 6 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// hex normalizes a base16 hex value to a leading-`#` form suitable for
// `lipgloss.Color`. Empty input → empty output (caller's problem).
func hex(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	return strings.ToLower(v)
}

// ToTheme maps base16 slots to sextant role tokens.
//
// The mapping is opinionated and documented inline. Conventions
// followed:
//
//   - base00 / base01 carry the chrome (`bg` / `bg_alt`).
//   - base05 / base03 carry text (`fg` / `fg_muted`).
//   - base03 / base0D carry pane edges (`border` / `border_active`):
//     muted-text shade is the right "inactive" weight; the
//     functions-blue is the canonical focus color.
//   - base0D is also `accent`. base16 puts blue at "functions" — the
//     same channel modern terminals reach for selection / focus, and
//     keeping `border_active` and `accent` synonymous matches the
//     conventions doc's "one signal color per screen" guidance.
//   - base08 / base09 / base0B map straight to danger / warning /
//     success. base0A (yellow) is unused: the role table only carries
//     three meaningful signal slots besides accent.
//
// If a scheme wants different bindings it can override after loading.
func (s Base16Scheme) ToTheme() Theme {
	c := func(v string) lipgloss.TerminalColor { return lipgloss.Color(hex(v)) }
	return Theme{
		Name:            s.Scheme,
		Background:      c(s.Base00),
		BackgroundAlt:   c(s.Base01),
		Foreground:      c(s.Base05),
		ForegroundMuted: c(s.Base03),
		Border:          c(s.Base03),
		BorderActive:    c(s.Base0D),
		Accent:          c(s.Base0D),
		Danger:          c(s.Base08),
		Warning:         c(s.Base09),
		Success:         c(s.Base0B),
	}
}
