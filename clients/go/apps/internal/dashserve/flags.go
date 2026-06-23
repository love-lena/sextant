package dashserve

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
)

// Flags are the web dash's command-line flags: the bus-connection flags
// (mirroring the operator CLI's connFlags shape — --creds/--store/--url/
// --context with the $SEXTANT_* defaults, ADR-0021) plus the serve flags
// (--port, --allow-origin, --ui, --state-file). The sextant-dash binary
// registers and resolves these.
type Flags struct {
	creds   *string
	store   *string
	url     *string
	context *string
	name    *string

	port            *int
	allowOrigin     *string
	ui              *string
	stateFile       *string
	operatorSession *bool
}

// AddFlags registers the web dash's flags on fs, defaulting from the
// environment the same way the operator CLI does. Call fs.Parse, then Resolve.
func AddFlags(fs *flag.FlagSet) *Flags {
	return &Flags{
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (issue with `sextant clients register`; or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "bus store dir for discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one; see `sextant context`)"),
		name:    fs.String("name", "", "display name a first-run self-enrollment registers under (default: $USER)"),

		port:            fs.Int("port", defaultServePort, "port for the web dash on 127.0.0.1 (0 picks a free port); the server is loopback-only"),
		allowOrigin:     fs.String("allow-origin", "", "comma-separated extra browser origins the server accepts (localhost is always allowed)"),
		ui:              fs.String("ui", "", "serve a custom frontend directory instead of the built-in embedded SPA (dev hook)"),
		stateFile:       fs.String("state-file", "", "path to write a JSON state file {url,token,port} on start and remove on clean shutdown (default: $SEXTANT_HOME/dash.json when managed by components)"),
		operatorSession: fs.Bool("operator-session", false, "mint the OPERATOR's browser session via the delegated path (ADR-0047) instead of this client's own — set by the managed component so a headless dash's page still acts as the operator"),
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
		CredsPath:       creds,
		URL:             url,
		Store:           *f.store,
		Name:            *f.name,
		Port:            *f.port,
		AllowedOrigins:  splitOrigins(*f.allowOrigin),
		UIDir:           *f.ui,
		StateFile:       *f.stateFile,
		OperatorSession: *f.operatorSession,
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

// defaultStore is the bus store dir the web dash uses when --store is not
// given: $SEXTANT_STORE if set, else the same per-user path the operator CLI
// uses, so the dash and `sextant up` share key material + discovery by default.
func defaultStore() string {
	if v := os.Getenv("SEXTANT_STORE"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}
