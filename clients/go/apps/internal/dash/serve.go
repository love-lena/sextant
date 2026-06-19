package dash

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
	"strconv"
	"time"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
	"github.com/love-lena/sextant/clients/go/sdk"
)

// Compile-time proof that the live SDK client satisfies the API's narrow Bus
// dependency: the server is fed the real *sextant.Client in production and a
// fake in the dashapi tests.
var _ dashapi.Bus = (*sextant.Client)(nil)

// defaultServePort is the loopback port the API binds when --port is not given:
// a fixed local port so the URL is predictable across launches.
const defaultServePort = 8765

// runServe runs the dash as a local HTTP API + web debug surface (ADR-0032)
// rather than the terminal UI. It resolves the SAME identity the TUI uses
// (ensureIdentity → Connect; zero-config first run included), then serves the
// API on a loopback listener behind a per-launch token, with the Go process the
// single bus client. It prints the browser URL (carrying the token) to out, and
// serves until ctx is cancelled — then drains the HTTP server and closes the bus
// client. announce output goes to out (stdout in production; a buffer in tests).
func runServe(ctx context.Context, opts Options, out io.Writer) error {
	if err := ensureIdentity(ctx, &opts, out); err != nil {
		return err
	}

	cctx, cancelConnect := context.WithTimeout(ctx, launchTimeout)
	defer cancelConnect()
	client, err := sextant.Connect(cctx, sextant.Options{
		CredsPath:    opts.CredsPath,
		URL:          opts.URL,
		ConnInfoPath: connInfoPath(opts.Store),
		Logf:         func(string, ...any) {},
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("bus connected but did not answer within %s — is it healthy? try `sextant up`: %w", launchTimeout, err)
		}
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

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

	// BaseContext makes every request context cancellable from here, so shutdown
	// can cancel in-flight requests — crucially the long-lived SSE streams, which
	// block on their request context and would otherwise hold the drain open.
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	api := dashapi.New(dashapi.Config{Bus: client, Token: token, AllowedOrigins: opts.AllowedOrigins, UIDir: opts.UIDir})
	// Track the subjects the dash sees (msg.>) so the UI can list conversations
	// on load, not just as new traffic arrives. Best-effort; failure is non-fatal.
	if err := api.Watch(srvCtx); err != nil {
		fmt.Fprintf(out, "sextant dash --serve: subject watch unavailable: %v\n", err)
	}
	srv := &http.Server{
		Handler:           api,
		BaseContext:       func(net.Listener) context.Context { return srvCtx },
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Build the tokenized URL once so both the announce line and the state file
	// use the same value.
	serveURL := fmt.Sprintf("http://%s/?token=%s", ln.Addr(), token)
	fmt.Fprintf(out, "sextant dash --serve: API + debug surface at %s\n", serveURL)
	fmt.Fprintf(out, "  (the URL carries the access token — keep it local; Ctrl-C to stop)\n")

	// Write the state file when a path was given. The file lets a managed/
	// backgrounded dash expose its URL without the operator having to capture
	// stdout (`sextant dash url` reads it). Written after the listener binds so
	// the port is final; removed on any return so absence always means not running
	// — the defer covers both clean-shutdown and unexpected-server-error paths.
	if opts.StateFile != "" {
		if err := writeStateFile(opts.StateFile, serveURL, token, ln.Addr()); err != nil {
			return fmt.Errorf("write state file: %w", err)
		}
		defer os.Remove(opts.StateFile) // best-effort; failure leaves a stale file but doesn't mask a serve error
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

// DashState is the on-disk record written by runServe when a StateFile is
// configured. It lets a managed/backgrounded dash expose its URL after launch.
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
// always 127.0.0.1 — there is no host knob, so the --serve API can never bind a
// routable interface (ADR-0032: the API is local-only). Port 0 lets the kernel
// pick a free port.
func serveAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
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
