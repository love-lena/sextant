// Package dashserve is the web dash's serve path (ADR-0044, ADR-0046): it
// connects under one resolved bus identity, mints the browser tab's session
// credential, and serves the embedded SPA + favicon over a loopback HTTP
// listener. It is the engine room of the sextant-dash binary, and is also
// imported by the `sextant` CLI for the bits the serve path and the `dash`
// verb share (the on-disk state record `sextant dash url` reads).
//
// The split from the terminal UI (internal/dash) is ADR-0046: the browser dash
// is THE dash; the terminal UI is a peer feature reached via sextant-tui. So
// the HTTP/serve path and the dashapi dependency live ONLY here, never in
// internal/dash.
package dashserve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/love-lena/sextant/clients/sextant-dash/dashapi"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/sdk/go"
	"github.com/love-lena/sextant/shared/go/selfenroll"
)

// Compile-time proof that the live SDK client satisfies the API's narrow Bus
// dependency: the connect-per-request minter wraps it (its connectOnce returns a
// *sextant.Client), and the dashapi tests feed a fake.
var _ sessionClient = (*sextant.Client)(nil)

// defaultServePort is the loopback port the API binds when --port is not given:
// a fixed local port so the URL is predictable across launches.
const defaultServePort = 8765

// launchTimeout bounds each launch I/O step — the first-run self-enrollment
// (connect + mint + context write) and the steady-state connect handshake — so
// a wedged or half-up bus fails loud instead of hanging the launch.
const launchTimeout = 10 * time.Second

// Options carries the resolved inputs Run needs: the identity (creds + URL +
// bus store for discovery) and the serve knobs. The caller (the sextant-dash
// binary) resolves creds/URL the same way the operator CLI does (explicit
// --creds/$SEXTANT_CREDS, else the active/named client context — ADR-0021). An
// EMPTY CredsPath means no identity was resolvable; Run then runs the
// zero-config first-run path (ADR-0024): if a local bus is discoverable under
// Store it self-enrolls, otherwise it fails loud.
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

	// Port is the loopback port the API binds (default 8765; 0 picks a free
	// port). The host is always 127.0.0.1 — the API is local-only, so only the
	// port is configurable.
	Port int
	// AllowedOrigins are extra browser origins the API accepts beyond localhost
	// (always allowed), for a separate dev server hosting the UI.
	AllowedOrigins []string
	// UIDir, when set, serves a custom frontend directory instead of the
	// built-in embedded SPA (the dev hook).
	UIDir string
	// StateFile, when set, is the path where Run writes a JSON state file on
	// start (url, token, port) and removes it on clean shutdown. The default
	// path when managed by the components layer is $SEXTANT_HOME/dash.json; this
	// field carries whatever the caller (flag or registry) resolved.
	StateFile string
	// OperatorSession selects the DELEGATED minter (ADR-0047): the managed dash
	// component runs under its own dash.creds (not connected as the operator), so
	// it cannot use clients.session (which would mint a dash-id session and
	// re-break the ADR-0044 routing). With this set Run mints the OPERATOR's
	// session via clients.session-operator instead, so the page still acts AS the
	// operator. Unset (the dev/foreground default) keeps the clients.session
	// self-mint UNCHANGED — only the components registry sets it.
	OperatorSession bool
}

// Run resolves the dash's identity, then serves the dash as a local HTTP API +
// embedded SPA (ADR-0044) holding NO standing bus connection (ADR-0046,
// TASK-187). It resolves the identity the same way the terminal UI does
// (ensureIdentity; zero-config first run included), but unlike the lift-and-shift
// slice it does NOT connect at startup: the only bus act left, minting a browser
// session credential, is done connect-per-request by the minter behind
// dashapi.Bus, so the dash has zero bus presence with no tab open. It serves the
// API on a loopback listener behind a per-launch token, prints the browser URL
// (carrying the token) to out, and serves until ctx is cancelled — then drains
// the HTTP server. announce output goes to out (stdout in production; a buffer in
// tests).
func Run(ctx context.Context, opts Options, out io.Writer) error {
	if err := ensureIdentity(ctx, &opts, out); err != nil {
		return err
	}

	token, err := newToken()
	if err != nil {
		return fmt.Errorf("mint access token: %w", err)
	}

	// The API is local-only: the host is always loopback, so only the port is
	// configurable and a routable interface can never be expressed (ADR-0032).
	addr := serveAddr(opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	// The bus WebSocket URL the browser dials (ADR-0044): the bus records it in the
	// discovery file when its WebSocket listener is on. The dash reads it and hands
	// it to the page at credential-mint time. Empty when the bus has no WebSocket
	// listener — the mint endpoint then fails loud with the remediation, and the
	// announce below warns the operator up front.
	wsURL := resolveWSURL(opts)
	if wsURL == "" {
		_, _ = fmt.Fprintf(out, "sextant-dash: the bus has no WebSocket listener — the browser dash cannot connect.\n"+
			"  enable it: `sextant config set ws-listen 127.0.0.1:7423` then restart the bus (ADR-0044).\n")
	}

	// BaseContext makes every request context cancellable from here, so shutdown
	// can cancel in-flight requests.
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	// The minter holds the resolved connection inputs, not a connection: it
	// connects, mints, and closes within each POST /api/session (ADR-0046), so the
	// Server carries no persistent bus client. The managed component (--operator-
	// session) mints the OPERATOR's session via the delegated path (ADR-0047); the
	// dev/foreground default mints for self via clients.session, unchanged.
	minter := selectMinter(opts)
	api := dashapi.New(dashapi.Config{Bus: minter, Token: token, WSURL: wsURL, AllowedOrigins: opts.AllowedOrigins, UIDir: opts.UIDir})
	srv := &http.Server{
		Handler:           api,
		BaseContext:       func(net.Listener) context.Context { return srvCtx },
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Build the tokenized URL once so both the announce line and the state file
	// use the same value.
	serveURL := fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token)
	_, _ = fmt.Fprintf(out, "sextant-dash: serving the web dash at %s\n", serveURL)
	_, _ = fmt.Fprintf(out, "  (the URL carries the access token — keep it local; Ctrl-C to stop)\n")

	// Write the state file when a path was given. The file lets a managed/
	// backgrounded dash expose its URL without the operator having to capture
	// stdout (`sextant dash url` reads it). Written after the listener binds so
	// the port is final; removed on any return so absence always means not running
	// — the defer covers both clean-shutdown and unexpected-server-error paths.
	if opts.StateFile != "" {
		if err := writeStateFile(opts.StateFile, serveURL, token, ln.Addr()); err != nil {
			return fmt.Errorf("write state file: %w", err)
		}
		defer func() { _ = os.Remove(opts.StateFile) }() // best-effort; failure leaves a stale file but doesn't mask a serve error
	}

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		srvCancel() // cancel in-flight request contexts (SSE streams return now)
		// Bounded drain so shutdown can never hang (fail-loud, never hang).
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = srv.Shutdown(sctx)
		return nil
	case err := <-serveErr:
		return err
	}
}

// selectMinter builds the dashapi.Bus Run serves through, choosing by
// Options.OperatorSession (ADR-0047). The managed component (--operator-session)
// gets the delegatedMinter — it runs under its own dash.creds and mints the
// OPERATOR's session via clients.session-operator, so the page acts AS the
// operator. The dev/foreground default gets the connectMinter (clients.session for
// self), built through the newMinter package-var seam the lifetime tests
// instrument — so the unchanged self-mint path stays observable end to end.
func selectMinter(opts Options) dashapi.Bus {
	connInfo := connInfoPath(opts.Store)
	if opts.OperatorSession {
		return newDelegatedMinter(opts.CredsPath, opts.URL, connInfo)
	}
	return newMinter(opts.CredsPath, opts.URL, connInfo)
}

// ensureIdentity gives Run an identity to connect as. With CredsPath already
// resolved (flags, env, or a context) it is a no-op. With none it is the
// zero-config first run (ADR-0024): when a local bus is discoverable (the
// bus.json discovery file under Store), it self-enrolls — same semantics as
// `sextant clients register --self`, named from $USER (Options.Name overrides),
// kind "human" (the dash is the human's seat) — and prints exactly one notice
// line to the given writer. The next run resolves the saved (now active)
// context silently. With no bus discoverable it fails loud with guidance, never
// hangs (the enrollment is deadline-bound).
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

// DashState is the on-disk record written by Run when a StateFile is
// configured. It lets a managed/backgrounded dash expose its URL after launch
// (read by `sextant dash` and `sextant dash url`).
type DashState struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Port  int    `json:"port"`
}

// writeStateFile writes a DashState JSON record to path with 0600 permissions.
// addr is the net.Addr returned by the listener (host:port).
func writeStateFile(path, url, token string, addr net.Addr) error {
	_, portStr, _ := net.SplitHostPort(addr.String())
	port, _ := strconv.Atoi(portStr)
	state := DashState{URL: url, Token: token, Port: port}
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ReadStateFile reads a DashState from path. Returns an error if the file is
// absent or unparseable.
func ReadStateFile(path string) (DashState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return DashState{}, err
	}
	var s DashState
	if err := json.Unmarshal(b, &s); err != nil {
		return DashState{}, fmt.Errorf("parse state file: %w", err)
	}
	return s, nil
}

// serveAddr is the loopback listen address for the given port. The host is
// always 127.0.0.1 — there is no host knob, so the API can never bind a
// routable interface (ADR-0032: the API is local-only). Port 0 lets the kernel
// pick a free port.
func serveAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// resolveWSURL discovers the bus WebSocket URL the browser dials (ADR-0044) from
// the local discovery file the bus writes when its WebSocket listener is on. Empty
// means no listener (or it could not be read) — the dash then warns up front and
// the mint endpoint fails loud with the remediation. The dash is a loopback
// surface, so the locally-discovered bus.json is the authoritative source; a
// remote-bus dash (--url) is out of scope for the browser WebSocket path.
func resolveWSURL(opts Options) string {
	info, err := conninfo.Read(connInfoPath(opts.Store))
	if err != nil {
		return ""
	}
	return info.WSURL
}

// newToken mints a per-launch bearer token: 32 bytes of crypto-random entropy,
// hex-encoded. It is the capability that gates every API call for this launch.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// connInfoPath is the discovery file under the bus store, or "" when no store is
// given (then Options.URL must carry the address).
func connInfoPath(store string) string {
	if store == "" {
		return ""
	}
	return filepath.Join(store, conninfo.DefaultFile)
}
