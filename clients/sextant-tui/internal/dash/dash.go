// Package dash is the terminal UI's composition root (ADR-0023/0024): it
// assembles the three master-detail browsers — clients, topics, artifacts —
// through the layout engine into the cockpit, holds the one bus identity, and
// runs the program. It is the only layer that may import everything below it —
// the TUI library (theme, surface, layout, busfeed), the public SDK, and the
// client-context resolution — because it is where the domain surfaces, the
// identity, and the layout meet.
//
// This is the engine room of the sextant-tui binary (clients/sextant-tui). The
// browser dash is THE dash now (ADR-0046); the terminal UI is a first-class peer
// feature, not a lesser dashboard, and it carries no serve/HTTP code — the web
// serve path lives only in dashserve (sextant-dash). The terminal UI is just
// another client over the SDK — forkable, no special privilege (ADR-0014,
// ADR-0008).
package dash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/layout"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/sdk/go"
	"github.com/love-lena/sextant/shared/go/selfenroll"
)

// ThemeChoice selects the cockpit's palette. Light/Dark force a palette; Auto
// detects the terminal background at every launch. Every explicit choice is
// persisted to the layout config — auto included, so `--theme auto` is the way
// back into detection after a concrete choice.
type ThemeChoice string

const (
	// ThemeAuto detects the terminal background at every launch (the fresh-config
	// default). Passed explicitly, it resets a persisted concrete theme back to
	// detection.
	ThemeAuto ThemeChoice = "auto"
	// ThemeLight forces the light palette.
	ThemeLight ThemeChoice = "light"
	// ThemeDark forces the dark palette.
	ThemeDark ThemeChoice = "dark"
)

// Valid reports whether c is one of the three known theme choices. The empty
// string is also valid: it means "not chosen this launch" — the persisted
// config's choice applies (auto on a fresh config). Resolve uses this to fail
// loud on a bad --theme rather than silently falling back to a surprising
// default.
func (c ThemeChoice) Valid() bool {
	switch c {
	case "", ThemeAuto, ThemeLight, ThemeDark:
		return true
	default:
		return false
	}
}

// Options carries the resolved inputs Run needs: the identity (creds + URL +
// bus store for discovery), the theme choice, and the layout config path. The
// caller (the binary or the alias) resolves creds/URL the same way the operator
// CLI does (explicit --creds/$SEXTANT_CREDS, else the active/named client
// context — ADR-0021). An EMPTY CredsPath means no identity was resolvable;
// Run then runs the zero-config first-run path (ADR-0024): if a local bus is
// discoverable under Store it self-enrolls, otherwise it fails loud.
type Options struct {
	// CredsPath is the dash's bus credential (its verified identity). Empty
	// triggers the first-run self-enrollment.
	CredsPath string
	// URL is the bus address; empty falls back to the discovery file under Store.
	URL string
	// Store is the bus store dir, used to find the bus.json discovery file when
	// URL is empty (and the enrollment credential on a first run).
	Store string
	// Name overrides the display name a first-run self-enrollment registers
	// under (--name); empty defaults from $USER (selfenroll.SelfName).
	Name string

	// Theme is the palette chosen THIS launch (auto/light/dark), persisted to
	// the layout config so it holds across runs: light/dark force a palette;
	// auto re-detects the terminal background at every launch (and, passed
	// explicitly, resets a persisted concrete choice back to detection). Empty
	// means no choice was made this launch — the persisted config's choice
	// applies, auto on a fresh config.
	Theme ThemeChoice
	// ConfigPath is where the layout config is loaded from and persisted to. Empty
	// disables persistence (a fresh DefaultConfig each run).
	ConfigPath string
}

// launchTimeout bounds each launch I/O step — the whole first-run
// self-enrollment (connect + mint + context write) and the steady-state
// connect handshake — so a wedged or half-up bus fails loud instead of
// hanging the launch.
const launchTimeout = 10 * time.Second

// Run connects under the resolved identity, assembles the cockpit, and runs the
// dash to completion. It is the shared entry point both faces call. With no
// identity resolved it first self-enrolls (zero-config first run, ADR-0024),
// printing its one notice line to stderr before entering the alt-screen. The
// ctx governs the connect handshake and scopes every surface's feeds/fetches;
// cancel it (or quit the dash) to wind down. On return the config has been
// persisted and the client closed.
func Run(ctx context.Context, opts Options) error {
	if err := ensureIdentity(ctx, &opts, os.Stderr); err != nil {
		return err
	}

	// The connect handshake is deadline-bound like every other launch I/O step
	// (fail-loud, never hang): the TCP dial is bounded by the NATS client's own
	// connect timeout, but a bus that ACCEPTS the connection and never answers
	// the hello (a wedged API responder) would otherwise block the launch
	// forever. Bounding is safe: sextant.Connect uses its ctx only for the
	// post-dial handshake (hello + the drain-subscription flush) — the
	// long-lived client is not tied to it — so the timeout cannot tear the
	// session down later.
	cctx, cancelConnect := context.WithTimeout(ctx, launchTimeout)
	defer cancelConnect()
	client, err := sextant.Connect(cctx, sextant.Options{
		CredsPath:    opts.CredsPath,
		URL:          opts.URL,
		ConnInfoPath: connInfoPath(opts.Store),
		// The dash is a TUI: a default log.Printf would scribble announcements onto
		// the alt-screen. Swallow them (the surfaces surface real errors in-pane).
		Logf: func(string, ...any) {},
	})
	if err != nil {
		// Our deadline firing (not a caller cancel) means the dial succeeded and
		// the handshake went unanswered — a different state from "no bus", so say so.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("bus at %s: connected but no answer after %s — is it healthy? try restarting it with `sextant up`: %w",
				selfenroll.ResolveBusURL(opts.URL, opts.Store), launchTimeout, err)
		}
		return fmt.Errorf("connect: %w", err)
	}

	// A child context that Run cancels the moment the program exits, by ANY quit
	// path. A bare `q`/options-menu quit ends the tea program without cancelling the
	// parent ctx, so the drain-watch goroutine (parked on Drained() ⊕ ctx.Done())
	// would otherwise leak. Cancelling progCtx on return unblocks it on every path;
	// the same ctx drives tea.WithContext so an external cancel still stops the loop.
	progCtx, cancelProg := context.WithCancel(ctx)
	defer cancelProg()

	r, err := build(progCtx, client, opts)
	if err != nil {
		_ = client.Close()
		return err
	}

	program := tea.NewProgram(r, tea.WithAltScreen(), tea.WithContext(progCtx))
	final, runErr := program.Run()
	cancelProg() // wind down the drain watch immediately, before teardown/Close

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
		return fmt.Errorf("run: %w", runErr)
	}
	return nil
}

// ensureIdentity gives Run an identity to connect as. With CredsPath already
// resolved (flags, env, or a context) it is a no-op. With none it is the
// zero-config first run (ADR-0024): when a local bus is discoverable (the
// bus.json discovery file under Store), it self-enrolls — same semantics as
// `sextant clients register --self`, named from $USER (Options.Name overrides),
// kind "human" (the dash is the human's seat) — and prints exactly one notice
// line to the given writer BEFORE the alt-screen opens. The next run resolves
// the saved (now active) context silently. With no bus discoverable it fails
// loud with guidance, never hangs (the enrollment is deadline-bound).
//
// Self-enrollment only works against the locally-discovered bus: it mints over
// the enroll.creds the bus provisioned under Store, and those creds belong to
// that bus alone. An explicit --url pointing anywhere else would enroll against
// the local bus and then dial the other one with the wrong creds — an auth
// failure AFTER a context was created and activated. So a mismatched (or
// undiscoverable) --url fails loud HERE, before any state is written; a --url
// that matches the discovered bus proceeds normally.
func ensureIdentity(ctx context.Context, opts *Options, notice io.Writer) error {
	if opts.CredsPath != "" {
		return nil
	}
	info, err := conninfo.Read(connInfoPath(opts.Store))
	if err != nil {
		if opts.URL != "" {
			return fmt.Errorf("no identity for --url %s — first-run self-enrollment only works against the locally-discovered bus (its enroll.creds under %s), and none was found: drop --url to enroll against a local `sextant up` bus, or for a remote bus pass --creds, or mint an identity there with `sextant clients register` and save it with `sextant context add`", opts.URL, opts.Store)
		}
		return fmt.Errorf("no identity and no local bus discovered under %s — run `sextant up` first (or pass --creds / select a context with `sextant context use`): %w", opts.Store, err)
	}
	if opts.URL != "" && opts.URL != info.URL {
		return fmt.Errorf("no identity, and --url %s is not the locally-discovered bus (%s, under %s) — first-run self-enrollment would mint against the local bus, leaving creds the --url bus rejects: drop --url to enroll locally, or for a remote bus pass --creds, or mint an identity there with `sextant clients register` and save it with `sextant context add`", opts.URL, info.URL, opts.Store)
	}
	ectx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()
	// The dash's first-run enrolls the operator's own human seat, so it also
	// claims the bus principal while it is still unclaimed (ADR-0031): zero-config
	// first run leaves the operator as the principal, no second command.
	res, err := selfenroll.Enroll(ectx, opts.Name, "human", info.URL, opts.Store, false, true)
	if err != nil {
		var ce *selfenroll.ErrContextExists
		if errors.As(err, &ce) {
			// The advice pins --kind human: the dash enrolls the human's seat, and
			// `register --self` defaults to kind "client" — following the advice
			// without it would silently re-enroll the seat under the wrong kind.
			return fmt.Errorf("context %q already exists — run `sextant context use %s` to adopt it, or `sextant clients register --self --kind human --force` to re-enroll", ce.Name, ce.Name)
		}
		return fmt.Errorf("first-run self-enroll: %w", err)
	}
	opts.CredsPath = res.CredsPath
	if opts.URL == "" {
		opts.URL = res.URL
	}
	_, _ = fmt.Fprintf(notice, "first run — enrolled as %s\n", res.Name)
	return nil
}

// build assembles the cockpit root: the resolved theme, the loaded config, and
// the three master-detail browsers (ADR-0024: clients · topics · artifacts,
// side by side) composed through the layout in the cockpit preset. It is split
// out so the e2e can drive the same root model against an embedded bus without
// going through Run's program loop.
func build(ctx context.Context, client *sextant.Client, opts Options) (root, error) {
	keys := theme.DefaultKeymap()

	cfg := layout.DefaultConfig()
	if opts.ConfigPath != "" {
		loaded, err := layout.LoadConfig(opts.ConfigPath)
		if err != nil {
			return root{}, fmt.Errorf("load config: %w", err)
		}
		cfg = loaded
	}
	// The theme choice to honour and persist: an explicit --theme this launch
	// wins (so a typed `--theme auto` resets a saved concrete theme back to
	// detection); otherwise the saved choice applies. cfg.Theme carries the
	// CHOICE — auto persists as auto, never as the variant it resolves to this
	// launch — so detection re-runs at every launch while the choice is auto.
	cfg.Theme = themeIntent(opts.Theme, cfg.Theme)

	// Resolve the choice into the render theme. The terminal-background probe
	// runs here, at the composition root, once per launch; the layout and the
	// surfaces receive a resolved concrete theme and never probe. In a non-tty
	// context (tests, pipes) the probe is skipped and auto resolves to dark,
	// deterministically.
	th := resolveTheme(cfg.Theme)

	// Resolve the conversations' author map from the directory (id → display name
	// + role), the seam ADR-0023 leaves open. The topics browser has no directory
	// of its own, so the dash threads the map in; the clients browser resolves
	// from its own live snapshots. A failure here is non-fatal — conversations
	// fall back to short author ids — so a slow or empty directory never blocks
	// launch.
	authors := resolveAuthors(ctx, client)

	clients := surface.NewClientsBrowser(ctx, client, th, keys)
	topics := surface.NewTopicsBrowser(ctx, client, th, keys,
		surface.WithConversationAuthors(authors))
	artifacts := surface.NewArtifactsBrowser(ctx, client, th, keys)

	m := layout.New(th, keys, cfg, clients, topics, artifacts)
	return newRoot(ctx, m, client), nil
}

// resolveAuthors builds the conversations' id → Author map from the clients
// directory, so authors render as display names in their role hue rather than
// raw ids. A directory read failure returns an empty map (the documented
// fallback), keeping launch non-blocking. The fetch is bounded by a 5-second
// deadline — matching the browsers' own fetch bound — so a connected-but-wedged
// bus fails fast rather than hanging the alt-screen open.
func resolveAuthors(ctx context.Context, client *sextant.Client) map[string]surface.Author {
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	infos, err := client.ListClients(fctx)
	if err != nil {
		return map[string]surface.Author{}
	}
	authors := make(map[string]surface.Author, len(infos))
	for _, ci := range infos {
		authors[ci.ID] = surface.Author{Name: ci.DisplayName, Role: ci.Kind}
	}
	return authors
}

// themeIntent picks the theme choice to honour and persist this launch: the
// explicitly passed flag value when there is one — an explicit auto
// deliberately overwrites a saved concrete choice; that is the way back into
// detection — else the saved choice. The result is always one of the three
// known variants, so an unknown value in a hand-edited config resolves to the
// default (auto) instead of round-tripping garbage.
func themeIntent(choice ThemeChoice, saved theme.Variant) theme.Variant {
	switch choice {
	case ThemeLight:
		return theme.VariantLight
	case ThemeDark:
		return theme.VariantDark
	case ThemeAuto:
		return theme.VariantAuto
	}
	switch saved {
	case theme.VariantLight, theme.VariantDark, theme.VariantAuto:
		return saved
	}
	return theme.VariantAuto
}

// resolveTheme maps a theme choice to a resolved concrete Theme. Auto probes
// the terminal background via theme.Auto — bounded by termenv's own query
// deadline, and skipped entirely (falling back to dark) when stdout is not a
// terminal, so a test or piped run never hangs on it.
func resolveTheme(v theme.Variant) theme.Theme {
	switch v {
	case theme.VariantLight:
		return theme.Light()
	case theme.VariantDark:
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
