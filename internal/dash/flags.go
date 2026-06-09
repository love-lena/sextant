package dash

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/internal/clictx"
)

// Flags are the dash's command-line flags: the bus-connection flags (mirroring
// the operator CLI's connFlags shape — --creds/--store/--url/--context with the
// $SEXTANT_* defaults, ADR-0021) plus the dash-specific flags (--theme,
// --config, --name). Both faces of the dash — cmd/sextant-dash and the
// `sextant dash` alias — register and resolve these the same way so they
// behave identically.
type Flags struct {
	creds   *string
	store   *string
	url     *string
	context *string

	theme  *string
	config *string
	name   *string
}

// AddFlags registers the dash flags on fs, defaulting from the environment the
// same way the operator CLI does. Call fs.Parse, then Resolve.
func AddFlags(fs *flag.FlagSet) *Flags {
	return &Flags{
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issue with `sextant clients register`; or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "bus store dir for discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one; see `sextant context`)"),

		theme:  fs.String("theme", "auto", "cockpit theme: light, dark, or auto"),
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
		return Options{}, fmt.Errorf("dash: invalid --theme %q (want light, dark, or auto)", *f.theme)
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
				return Options{}, fmt.Errorf("dash: context %q: %w", name, err)
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
