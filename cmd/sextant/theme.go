// theme.go owns the `sextant theme` resource noun. Surfaces the
// pkg/theme/ package (role tokens, base16 loader, defaults) through
// three verbs:
//
//	list   — enumerate themes in $XDG_CONFIG_HOME/sextant/themes/.
//	import — copy a base16 YAML into the themes dir after validating it.
//	show   — preview a theme's role tokens.
//
// Per `plans/issues/feat-sextant-theme-cobra-subcommand.md`. Pairs with
// the pkg/theme package (commit 7e36bef).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/love-lena/sextant/pkg/cliout"
	"github.com/love-lena/sextant/pkg/theme"
)

// newThemeCmd builds the `sextant theme` parent command.
func newThemeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "theme",
		Short: "Manage TUI themes (base16 YAML)",
		Long: `Inspect, import, and preview the themes pkg/theme/ exposes. Themes
live in $XDG_CONFIG_HOME/sextant/themes/ as base16 YAML; the active
theme is selected via the config.toml ` + "`theme`" + ` key or the
SEXTANT_THEME env var.`,
	}
	cmd.AddCommand(newThemeListCmd())
	cmd.AddCommand(newThemeImportCmd())
	cmd.AddCommand(newThemeShowCmd())
	return cmd
}

// themeEntry is the per-theme record `theme list` emits.
type themeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func newThemeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List themes available in $XDG_CONFIG_HOME/sextant/themes/",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := theme.DefaultThemesDir()
			if err != nil {
				return err
			}
			entries, err := listThemeFiles(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				if err := writeJSON(cmd, out, entries); err != nil {
					return err
				}
				if len(entries) == 0 {
					return errNoResults
				}
				return nil
			}
			if len(entries) == 0 {
				if _, err := fmt.Fprintf(out, "no themes in %s\n", dir); err != nil {
					return err
				}
				return errNoResults
			}
			for _, e := range entries {
				printf(out, "%s\t%s\n", e.Name, e.Path)
			}
			return nil
		},
	}
}

// listThemeFiles enumerates *.yaml / *.yml entries in the themes dir.
// Returns an empty slice (not an error) when the dir doesn't exist —
// new installs haven't had `sextant theme import` run against them yet.
func listThemeFiles(dir string) ([]themeEntry, error) {
	infos, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []themeEntry{}, nil
		}
		return nil, fmt.Errorf("read themes dir %s: %w", dir, err)
	}
	out := make([]themeEntry, 0, len(infos))
	for _, info := range infos {
		if info.IsDir() {
			continue
		}
		name := info.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		out = append(out, themeEntry{
			Name: strings.TrimSuffix(name, filepath.Ext(name)),
			Path: filepath.Join(dir, name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func newThemeImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <path>",
		Short: "Copy a base16 YAML into $XDG_CONFIG_HOME/sextant/themes/",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			raw, err := os.ReadFile(src) //nolint:gosec // operator-supplied path
			if err != nil {
				return fmt.Errorf("read %s: %w", src, err)
			}
			// Validate the file parses as base16 before we copy it in.
			if _, err := theme.ParseBase16(raw); err != nil {
				return fmt.Errorf("parse base16: %w", err)
			}
			dir, err := theme.DefaultThemesDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
			dst := filepath.Join(dir, filepath.Base(src))
			if err := os.WriteFile(dst, raw, 0o644); err != nil { //nolint:gosec // theme files are non-secret
				return fmt.Errorf("write %s: %w", dst, err)
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, themeEntry{
					Name: strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)),
					Path: dst,
				})
			}
			printf(out, "imported %s → %s\n", src, dst)
			return nil
		},
	}
}

func newThemeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Print the active theme's role tokens (or the named theme's)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, name, err := resolveThemeForShow(args)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if globalFlags.asJSON {
				return writeJSON(cmd, out, themeShowPayload{
					Name: name,
					Roles: []string{
						"bg", "bg_alt", "fg", "fg_muted", "border", "border_active",
						"accent", "danger", "warning", "success",
					},
				})
			}
			return renderThemeShow(out, name, t)
		},
	}
}

// themeShowPayload is the --json shape of `sextant theme show`. The
// theme's role values are lipgloss.TerminalColor interface — not
// JSON-marshalable without help — so we surface the role names + the
// theme's display name only. A future iteration can serialize the
// resolved hex codes by exposing a helper from pkg/theme/.
type themeShowPayload struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// resolveThemeForShow loads the theme named by args[0], or the default
// theme when args is empty. Returns the theme + a display name.
func resolveThemeForShow(args []string) (theme.Theme, string, error) {
	if len(args) == 0 {
		return theme.DefaultTheme(), "default", nil
	}
	dir, err := theme.DefaultThemesDir()
	if err != nil {
		return theme.Theme{}, "", err
	}
	name := args[0]
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, name+ext)
		if _, err := os.Stat(path); err == nil {
			t, err := theme.LoadBase16(path)
			if err != nil {
				return theme.Theme{}, name, fmt.Errorf("load %s: %w", path, err)
			}
			return t, name, nil
		}
	}
	return theme.Theme{}, name, errUserUsage(
		fmt.Sprintf("theme %q not found in %s (try `sextant theme list`)", name, dir),
	)
}

// renderThemeShow writes a small role-token table to w. lipgloss
// TerminalColor doesn't expose its hex via a stable API, so we just
// surface that the role exists + use the theme's own styles to render
// "ROLE" as a swatch when rendered to a TTY.
func renderThemeShow(w io.Writer, name string, t theme.Theme) error {
	printf(w, "theme: %s\n", name)
	if t.Empty() {
		printf(w, "  (theme has no roles populated — pkg/theme returned an empty Theme)\n")
		return nil
	}
	printf(w, "  structural:\n")
	printf(w, "    bg              bg_alt          fg              fg_muted\n")
	printf(w, "    border          border_active\n")
	printf(w, "  signal:\n")
	printf(w, "    accent          danger          warning         success\n")
	return nil
}

// _ keeps the cliout import live across small refactors; writeJSON
// in agents.go pulls the package transitively so theme.go's direct
// reference here is belt-and-braces.
var _ = cliout.EnvelopeVersion
