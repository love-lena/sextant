package dashserve

import (
	"context"
	"errors"
	"fmt"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
	"github.com/love-lena/sextant/clients/go/sdk"
)

// delegatedMinter is the MANAGED dash component's bridge to the bus (ADR-0047,
// TASK-188). Like connectMinter it satisfies dashapi.Bus holding NO standing
// connection — only the resolved inputs to make one — but it mints the OPERATOR's
// session rather than its own: each MintSession opens a fresh ISSUER connection
// over the dash's own dash.creds, calls the delegated mint
// (Issuer.MintOperatorSession → clients.session-operator), and closes the
// connection before returning. So the headless dash hands the browser a credential
// that acts AS the operator — preserving the ADR-0044 routing once the foreground
// dash is gone — while itself holding nothing but a narrow loopback capability.
//
// It is selected only by an EXPLICIT --operator-session flag (the managed
// component sets it); the dev/foreground dash keeps connectMinter (clients.session
// for self) UNCHANGED. There is deliberately no cache and no pooled connection (the
// same connect-per-request discipline as connectMinter): one loopback connect per
// tab-open, accepted not optimized.
type delegatedMinter struct {
	credsPath    string
	url          string
	connInfoPath string

	// connect opens a delegated-mint issuer connection from the held inputs. It is
	// a field so a test can inject a double that counts connects and verifies Close
	// ran before MintSession returned; production leaves it nil and connectOnce
	// falls back to the real SDK ConnectIssuer below.
	connect func(ctx context.Context) (issuerClient, error)
}

// issuerClient is the slice of *sextant.Issuer the delegated minter drives within
// one request: mint the operator's session credential, then close. Defining it
// here (where it is consumed) keeps the connect step injectable for the lifetime
// test without widening the SDK's surface.
type issuerClient interface {
	MintOperatorSession(ctx context.Context) (sextant.IssuedClient, error)
	Close() error
}

// newDelegatedMinter builds the per-request delegated minter from the resolved
// connection inputs (creds, URL, discovery file). It holds inputs, not a
// connection — the returned value satisfies dashapi.Bus and connects only when
// MintSession is called.
func newDelegatedMinter(credsPath, url, connInfoPath string) *delegatedMinter {
	return &delegatedMinter{credsPath: credsPath, url: url, connInfoPath: connInfoPath}
}

var _ dashapi.Bus = (*delegatedMinter)(nil)

// Compile-time proof that the live SDK issuer satisfies the delegated minter's
// narrow issuerClient dependency: the connect-per-request path returns a
// *sextant.Issuer.
var _ issuerClient = (*sextant.Issuer)(nil)

// MintSession connects an issuer, mints the OPERATOR's session credential, and
// closes — all within the call (ADR-0047). The connect is deadline-bound by
// launchTimeout so a wedged bus fails loud instead of hanging the request. Close
// runs via defer so the connection is gone before MintSession returns, on the
// error path too.
func (m *delegatedMinter) MintSession(ctx context.Context) (sextant.IssuedClient, error) {
	cctx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()

	iss, err := m.connectOnce(cctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return sextant.IssuedClient{}, fmt.Errorf("bus connected but did not answer within %s — is it healthy? try `sextant up`: %w", launchTimeout, err)
		}
		return sextant.IssuedClient{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = iss.Close() }()

	return iss.MintOperatorSession(ctx)
}

// connectOnce opens a delegated-mint issuer connection: the injected connect when
// a test supplied one, otherwise the real SDK ConnectIssuer.
func (m *delegatedMinter) connectOnce(ctx context.Context) (issuerClient, error) {
	if m.connect != nil {
		return m.connect(ctx)
	}
	return sextant.ConnectIssuer(ctx, sextant.Options{
		CredsPath:    m.credsPath,
		URL:          m.url,
		ConnInfoPath: m.connInfoPath,
	})
}
