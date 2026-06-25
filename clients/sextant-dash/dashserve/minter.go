package dashserve

import (
	"context"
	"errors"
	"fmt"

	"github.com/love-lena/sextant/clients/sextant-dash/dashapi"
	"github.com/love-lena/sextant/sdk/go"
)

// connectMinter is the dash's connect-per-request bridge to the bus (ADR-0046,
// TASK-187): it satisfies dashapi.Bus while holding NO standing connection — only
// the resolved inputs to make one. Each MintSession opens a fresh connection,
// mints the browser session credential over it (the clients.session round-trip),
// and closes the connection before returning, so the dash has zero bus presence
// at rest. The only client the bus ever sees is an open browser tab plus this
// brief per-mint blip. There is deliberately no cache and no pooled connection
// (AC#4): mint latency is one loopback connect per tab-open, accepted not
// optimized.
type connectMinter struct {
	credsPath    string
	url          string
	connInfoPath string

	// logf, when set, receives the per-mint connection's SDK announce log. It is
	// off (nil → silenced) in production — the dash routes nothing through the
	// announce path — and set in the AC#3 test so the connection's stderr is
	// observable, to prove no `sx.hb` permissions violation is raised.
	logf func(string, ...any)

	// connect opens a session-minting connection from the held inputs. It is a
	// field so a test can inject a double that counts connects and verifies Close
	// ran before MintSession returned; production leaves it nil and connectOnce
	// falls back to the real SDK connect below.
	connect func(ctx context.Context) (sessionClient, error)
}

// sessionClient is the slice of *sextant.Client the minter drives within one
// request: mint the session credential, then close. Defining it here (where it is
// consumed) keeps the connect step injectable for the lifetime test without
// widening the SDK's surface.
type sessionClient interface {
	MintSession(ctx context.Context) (sextant.IssuedClient, error)
	Close() error
}

// newConnectMinter builds the per-request minter from the resolved connection
// inputs (creds, URL, discovery file). It holds inputs, not a connection — the
// returned value satisfies dashapi.Bus and connects only when MintSession is
// called.
func newConnectMinter(credsPath, url, connInfoPath string) *connectMinter {
	return &connectMinter{credsPath: credsPath, url: url, connInfoPath: connInfoPath}
}

// newMinter is the seam Run builds its dashapi.Bus through. It is a package var,
// not a direct call, so a same-package test can wrap it to instrument the
// minter's connect/close (counting connections, capturing the connection log) and
// prove the connection-lifetime contract end to end through the real serve path.
// Production is exactly newConnectMinter.
var newMinter = newConnectMinter

var _ dashapi.Bus = (*connectMinter)(nil)

// MintSession connects, mints a session credential, and closes — all within the
// call (ADR-0046). The connect is deadline-bound by launchTimeout so a wedged bus
// fails loud instead of hanging the request. Close runs via defer so the
// connection is gone before MintSession returns, on the error path too.
func (m *connectMinter) MintSession(ctx context.Context) (sextant.IssuedClient, error) {
	cctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()

	client, err := m.connectOnce(cctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return sextant.IssuedClient{}, fmt.Errorf("bus connected but did not answer within %s — is it healthy? try `sextant up`: %w", launchTimeout, err)
		}
		return sextant.IssuedClient{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = client.Close() }()

	return client.MintSession(ctx)
}

// connectOnce opens a session-minting connection: the injected connect when a
// test supplied one, otherwise the real SDK connect.
func (m *connectMinter) connectOnce(ctx context.Context) (sessionClient, error) {
	if m.connect != nil {
		return m.connect(ctx)
	}
	return m.realConnect(ctx)
}

// realConnect makes a fresh SDK connection over the held inputs, with the SDK's
// announce log routed through m.logf (silenced when unset — the dash routes
// nothing through the announce path in production). It never consults m.connect,
// so a test that wraps it to count connects calls the real connect, not itself.
func (m *connectMinter) realConnect(ctx context.Context) (sessionClient, error) {
	logf := m.logf
	if logf == nil {
		logf = func(string, ...any) {} // silence the announce path in production
	}
	return sextant.Connect(ctx, sextant.Options{
		CredsPath:    m.credsPath,
		URL:          m.url,
		ConnInfoPath: m.connInfoPath,
		Logf:         logf,
	})
}
