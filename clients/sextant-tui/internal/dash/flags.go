package dash

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/shared/go/clictx"
)

// Flags are the terminal UI's command-line flags: the bus-connection flags
// (mirroring the operator CLI's connFlags shape — --creds/--store/--url/
// --context with the $SEXTANT_* defaults, ADR-0021) plus the terminal-UI flags
// (--theme, --config, --name). The sextant-tui binary registers and resolves
// these. The web serve path is a separate binary (sextant-dash) with its own
// flags (dashserve), per ADR-0046.
type Flags struct {
	// fs is the flag set the flags were registered on, kept so Resolve can ask
	// which flags were EXPLICITLY passed (fs.Visit) — the theme flag's behaviour
	// differs between "not passed" (follow the persisted config) and "passed as
	// its default value" (`--theme auto` resets a persisted concrete theme).
	fs *flag.FlagSet

	creds   *string
	store   *string
	url     *string
	context *string

	theme  *string
	config *string
	name   *string
}

// AddFlags registers the terminal-UI flags on fs, defaulting from the
// environment the same way the operator CLI does. Call fs.Parse, then Resolve.
func AddFlags(fs *flag.FlagSet) *Flags {
	return &Flags{
		fs:      fs,
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issue with `sextant clients register`; or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "bus store dir for discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one; see `sextant context`)"),

		theme:  fs.String("theme", "auto", "cockpit theme: light, dark, or auto (re-detect the terminal background each launch); an explicit value is persisted, otherwise the saved choice applies"),
		config: fs.String("config", defaultConfigPath(), "layout config file (preset, hidden panes, theme); persisted on quit"),
		name:   fs.String("name", "", "display name a first-run self-enrollment registers under (default: $USER)"),
	}
}

// Resolve turns the parsed flags into Options, resolving the identity with the
// same precedence as the operator CLI (ADR-0021): an explicit --creds /
// $SEXTANT_CREDS wins (URL then from --url or --store discovery); otherwise a
// context — named by --context / $SEXTANT_CONTEXT, else the active one —
// supplies creds + URL. An explicit --url still overrides a context's URL.
// With NOTHING naming an identity it does not fail: it returns Options with an
// empty CredsPath, and Run handles the zero-config first run (self-enroll
// against a discoverable local bus, or fail loud with guidance — ADR-0024). A
// context named explicitly but unloadable is still a loud error.
func (f *Flags) Resolve() (Options, error) {
	th := ThemeChoice(*f.theme)
	if !th.Valid() {
		return Options{}, fmt.Errorf("invalid --theme %q (want light, dark, or auto)", *f.theme)
	}
	// An untouched --theme is "no choice this launch" (empty), so the persisted
	// config's choice applies. Only an EXPLICIT --theme carries through — which
	// is what lets a typed `--theme auto` (identical in value to the untouched
	// default) reset a persisted concrete theme back to detection.
	if !f.explicitlySet("theme") {
		th = ""
	}

	creds, url := *f.creds, *f.url
	if creds == "" {
		name := *f.context
		if name == "" {
			name = clictx.Active()
		}
		if name != "" {
			c, err := clictx.Load(name)
			if err != nil {
				return Options{}, fmt.Errorf("context %q: %w", name, err)
			}
			creds = c.Creds
			if url == "" {
				url = c.URL
			}
		}
	}
	return Options{
		CredsPath:  creds,
		URL:        url,
		Store:      *f.store,
		Name:       *f.name,
		Theme:      th,
		ConfigPath: *f.config,
	}, nil
}

// explicitlySet reports whether the named flag was passed on the command line
// (flag.Visit walks only the flags that were set), distinguishing an explicit
// choice from the flag's untouched default value.
func (f *Flags) explicitlySet(name string) bool {
	set := false
	f.fs.Visit(func(fl *flag.Flag) {
		if fl.Name == name {
			set = true
		}
	})
	return set
}

// defaultStore is the bus store dir a dash uses when --store is not given:
// $SEXTANT_STORE if set, else the same per-user path the operator CLI uses, so
// the dash and `sextant up` share key material + discovery by default.
func defaultStore() string {
	if v := os.Getenv("SEXTANT_STORE"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}

// defaultConfigPath is where the layout config persists by default: under the
// client-config root (the same root contexts live in, $SEXTANT_HOME-aware), so a
// hermetic test can pin it via $SEXTANT_HOME and a real run keeps it beside the
// other client state.
func defaultConfigPath() string {
	return filepath.Join(clictx.Root(), "dash", "layout.json")
}
