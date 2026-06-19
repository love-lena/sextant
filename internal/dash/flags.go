package dash

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/love-lena/sextant/internal/clictx"
)

// Flags are the dash's command-line flags: the bus-connection flags (mirroring
// the operator CLI's connFlags shape — --creds/--store/--url/--context with the
// $SEXTANT_* defaults, ADR-0021) plus the dash-specific flags (--theme,
// --config, --name). Both faces of the dash — cmd/sextant-dash and the
// `sextant dash` alias — register and resolve these the same way so they
// behave identically.
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

	serve       *bool
	port        *int
	allowOrigin *string
	ui          *string
	stateFile   *string
}

// AddFlags registers the dash flags on fs, defaulting from the environment the
// same way the operator CLI does. Call fs.Parse, then Resolve.
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

		serve:       fs.Bool("serve", false, "run a local HTTP API + web debug surface (127.0.0.1) instead of the terminal UI"),
		port:        fs.Int("port", defaultServePort, "port for the --serve API on 127.0.0.1 (0 picks a free port); the API is loopback-only"),
		allowOrigin: fs.String("allow-origin", "", "comma-separated extra browser origins the --serve API accepts (localhost is always allowed)"),
		ui:          fs.String("ui", "", "serve a custom frontend directory with --serve instead of the built-in debug surface"),
		stateFile:   fs.String("state-file", "", "path to write a JSON state file {url,token,port} on start and remove on clean shutdown (default: $SEXTANT_HOME/dash.json when managed by components)"),
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
		CredsPath:      creds,
		URL:            url,
		Store:          *f.store,
		Name:           *f.name,
		Theme:          th,
		ConfigPath:     *f.config,
		Serve:          *f.serve,
		Port:           *f.port,
		AllowedOrigins: splitOrigins(*f.allowOrigin),
		UIDir:          *f.ui,
		StateFile:      *f.stateFile,
	}, nil
}

// splitOrigins parses the comma-separated --allow-origin value into a trimmed,
// non-empty list (empty input yields nil).
func splitOrigins(s string) []string {
	if s == "" {
		return nil
	}
	var origins []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
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
