package dash

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/love-lena/sextant/internal/dashapi"
	"github.com/love-lena/sextant/pkg/sextant"
)

// Compile-time proof that the live SDK client satisfies the API's narrow Bus
// dependency: the server is fed the real *sextant.Client in production and a
// fake in the dashapi tests.
var _ dashapi.Bus = (*sextant.Client)(nil)

// defaultServeAddr is the loopback address the API binds when --addr is not
// given: a fixed local port so the URL is predictable across launches.
const defaultServeAddr = "127.0.0.1:8765"

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

	// Force loopback: the API is local-only, never bound to a routable address.
	addr := opts.Addr
	if addr == "" {
		addr = defaultServeAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	// BaseContext makes every request context cancellable from here, so shutdown
	// can cancel in-flight requests — crucially the long-lived SSE streams, which
	// block on their request context and would otherwise hold the drain open.
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	srv := &http.Server{
		Handler:           dashapi.New(dashapi.Config{Bus: client, Token: token, AllowedOrigins: opts.AllowedOrigins, UIDir: opts.UIDir}),
		BaseContext:       func(net.Listener) context.Context { return srvCtx },
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(out, "sextant dash --serve: API + debug surface at http://%s/?token=%s\n", ln.Addr(), token)
	fmt.Fprintf(out, "  (the URL carries the access token — keep it local; Ctrl-C to stop)\n")

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

// newToken mints a per-launch bearer token: 32 bytes of crypto-random entropy,
// hex-encoded. It is the capability that gates every API call for this launch.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
