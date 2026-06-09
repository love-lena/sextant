// Package dash is the dash's composition root (ADR-0023, 7.5): it assembles the
// M4 pane-surfaces through the layout engine into the cockpit, holds the one bus
// identity, and runs the program. It is the only layer that may import
// everything below it — the TUI library (theme, surface, layout, busfeed), the
// public SDK (pkg/sextant), and the client-context resolution — because it is
// where the domain surfaces, the identity, and the layout meet.
//
// Both faces of the dash share this one Run: the standalone binary
// (cmd/sextant-dash) and the `sextant dash` alias each resolve creds/URL and
// call dash.Run, so there is one implementation, not a binary plus a fragile
// exec-on-PATH. The dash is just another client over the SDK — forkable, no
// special privilege (ADR-0014, ADR-0008).
package dash

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// ThemeChoice selects the cockpit's palette. Auto detects the terminal
// background; Light/Dark force a palette.
type ThemeChoice string

const (
	// ThemeAuto detects the terminal background (the default).
	ThemeAuto ThemeChoice = "auto"
	// ThemeLight forces the light palette.
	ThemeLight ThemeChoice = "light"
	// ThemeDark forces the dark palette.
	ThemeDark ThemeChoice = "dark"
)

// Options carries the resolved inputs Run needs: the identity (creds + URL +
// bus store for discovery), the theme choice, the layout config path, and the
// default topic the cockpit's stream participates in. The caller (the binary or
// the alias) resolves creds/URL the same way the operator CLI does (explicit
// --creds/$SEXTANT_CREDS, else the active/named client context — ADR-0021).
type Options struct {
	// CredsPath is the dash's bus credential (its verified identity). Required.
	CredsPath string
	// URL is the bus address; empty falls back to the discovery file under Store.
	URL string
	// Store is the bus store dir, used to find the bus.json discovery file when
	// URL is empty.
	Store string

	// Theme selects the palette (auto/light/dark). Empty resolves to auto.
	Theme ThemeChoice
	// ConfigPath is where the layout config is loaded from and persisted to. Empty
	// disables persistence (a fresh DefaultConfig each run).
	ConfigPath string
	// Topic is the message topic the cockpit's stream observes and participates in
	// (subject sx.TopicSubject(Topic)). Empty resolves to DefaultTopic.
	Topic string
	// Artifact is the document the artifact + detail panes open on. Empty resolves
	// to DefaultArtifact.
	Artifact string
}

const (
	// DefaultTopic is the cockpit's default stream topic when none is given.
	DefaultTopic = "plan"
	// DefaultArtifact is the document the artifact panes open on by default.
	DefaultArtifact = "the-plan"
)

// ErrNoIdentity is the "you didn't say who to connect as" error, mirroring the
// CLI's errNoIdentity so a missing identity fails loud with the same guidance.
var ErrNoIdentity = errors.New("dash: no credentials (pass --creds, set $SEXTANT_CREDS, or select a context with `sextant context use <name>`)")

// Run connects under the resolved identity, assembles the cockpit, and runs the
// dash to completion. It is the shared entry point both faces call. The ctx
// governs the connect handshake and scopes every surface's feeds/fetches; cancel
// it (or quit the dash) to wind down. On return the config has been persisted
// and the client closed.
func Run(ctx context.Context, opts Options) error {
	if opts.CredsPath == "" {
		return ErrNoIdentity
	}

	client, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    opts.CredsPath,
		URL:          opts.URL,
		ConnInfoPath: connInfoPath(opts.Store),
		// The dash is a TUI: a default log.Printf would scribble announcements onto
		// the alt-screen. Swallow them (the surfaces surface real errors in-pane).
		Logf: func(string, ...any) {},
	})
	if err != nil {
		return fmt.Errorf("dash: connect: %w", err)
	}

	r, err := build(ctx, client, opts)
	if err != nil {
		_ = client.Close()
		return err
	}

	program := tea.NewProgram(r, tea.WithAltScreen(), tea.WithContext(ctx))
	final, runErr := program.Run()

	// Host teardown runs once, after the program exits, regardless of which quit
	// path fired (operator quit, ctrl+c, options-menu quit, or a bus drain): the
	// layout already tore down the surfaces on its quit path, so this persists the
	// config and closes the client. Done here (not in Update) so it is exactly-once.
	if rm, ok := final.(root); ok {
		rm.m.Stop() // idempotent; covers a quit path that bypassed the layout's Stop
		saveConfig(opts.ConfigPath, rm.m.Config())
	}
	_ = client.Close()

	if runErr != nil {
		return fmt.Errorf("dash: run: %w", runErr)
	}
	return nil
}

// build assembles the cockpit root: the resolved theme, the loaded config, the
// three M4 surfaces (presence, the participating stream, the artifact reader)
// plus the retargetable detail pane, composed through the layout in the cockpit
// preset. It is split out so the e2e can drive the same root model against an
// embedded bus without going through Run's program loop.
func build(ctx context.Context, client *sextant.Client, opts Options) (root, error) {
	th := resolveTheme(opts.Theme)
	keys := theme.DefaultKeymap()

	cfg := layout.DefaultConfig()
	if opts.ConfigPath != "" {
		loaded, err := layout.LoadConfig(opts.ConfigPath)
		if err != nil {
			return root{}, fmt.Errorf("dash: load config: %w", err)
		}
		cfg = loaded
	}
	// A persisted theme variant is honoured by the layout (apply overrides th's
	// variant); pin the loaded variant onto the surfaces too, since a surface
	// resolves its hues at construction. An explicit --theme overrides the config.
	if opts.Theme == "" || opts.Theme == ThemeAuto {
		if cfg.Theme == theme.VariantLight || cfg.Theme == theme.VariantDark {
			th = theme.New(cfg.Theme)
		}
	} else {
		cfg.Theme = th.Variant
	}

	topic := opts.Topic
	if topic == "" {
		topic = DefaultTopic
	}
	artifactName := opts.Artifact
	if artifactName == "" {
		artifactName = DefaultArtifact
	}

	subject := sx.TopicSubject(topic)

	// Resolve the stream's author map from presence (id → display name + role), the
	// seam ADR-0023 leaves open. A failure here is non-fatal — the stream falls back
	// to short author ids — so a slow or empty directory never blocks launch.
	authors := resolveAuthors(ctx, client)

	presence := surface.NewPresence(ctx, client, th, keys)
	stream := surface.NewStream(ctx, client, subject, th, keys,
		surface.WithCompose(), surface.WithAuthors(authors))
	artifact := surface.NewArtifact(ctx, client, artifactName, th, keys)
	detail := newDetail(ctx, client, artifactName, th, keys)

	m := layout.New(th, keys, cfg, presence, stream, artifact, detail)
	return newRoot(m, client, detail), nil
}

// resolveAuthors builds the stream's id → Author map from the clients directory,
// so stream authors render as display names in their role hue rather than raw
// ids. A directory read failure returns an empty map (the documented fallback),
// keeping launch non-blocking.
func resolveAuthors(ctx context.Context, client *sextant.Client) map[string]surface.Author {
	infos, err := client.ListClients(ctx)
	if err != nil {
		return map[string]surface.Author{}
	}
	authors := make(map[string]surface.Author, len(infos))
	for _, ci := range infos {
		authors[ci.ID] = surface.Author{Name: ci.DisplayName, Role: ci.Kind}
	}
	return authors
}

// resolveTheme maps a ThemeChoice to a concrete Theme (auto detects the terminal
// background).
func resolveTheme(c ThemeChoice) theme.Theme {
	switch c {
	case ThemeLight:
		return theme.Light()
	case ThemeDark:
		return theme.Dark()
	default:
		return theme.Auto()
	}
}

// connInfoPath is the discovery file under the bus store, or "" when no store is
// given (then Options.URL must carry the address).
func connInfoPath(store string) string {
	if store == "" {
		return ""
	}
	return filepath.Join(store, conninfo.DefaultFile)
}

// saveConfig persists the layout config, swallowing a write error: the dash is
// already exiting, so a failed config save must not block teardown (the one
// place fail-loud yields to a clean exit).
func saveConfig(path string, cfg layout.Config) {
	if path == "" {
		return
	}
	_ = layout.SaveConfig(path, cfg)
}
