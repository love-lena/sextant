package dashserve

import (
	"context"
	"errors"
	"flag"
	"testing"

	"github.com/love-lena/sextant/sdk/go"
)

// fakeIssuerClient is the connect double the delegated-minter test injects: it
// records that MintOperatorSession ran and that Close ran, and the order between
// them, so the test can assert the issuer connection was closed before MintSession
// returned to the caller — the same connect-per-request + deferred-Close contract
// connectMinter holds, but over the delegated operator-session mint.
type fakeIssuerClient struct {
	closed              bool
	mintReturned        bool
	closedAfterMintBody bool
}

func (f *fakeIssuerClient) MintOperatorSession(context.Context) (sextant.IssuedClient, error) {
	f.mintReturned = true
	return sextant.IssuedClient{ID: "01OPERATOR", Creds: "operator-session-creds"}, nil
}

func (f *fakeIssuerClient) Close() error {
	f.closed = true
	f.closedAfterMintBody = f.mintReturned
	return nil
}

// TestDelegatedMintConnectsAndClosesPerCall: the delegated minter connects an
// issuer per MintSession, mints the OPERATOR's session over it, returns that
// credential (id = the operator's, from MintOperatorSession), and closes the issuer
// connection before returning — even though Close is deferred.
func TestDelegatedMintConnectsAndClosesPerCall(t *testing.T) {
	var connects int
	fake := &fakeIssuerClient{}
	m := newDelegatedMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (issuerClient, error) {
		connects++
		return fake, nil
	}

	issued, err := m.MintSession(context.Background())
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if connects != 1 {
		t.Fatalf("connect called %d times, want exactly 1 per mint", connects)
	}
	if issued.ID != "01OPERATOR" || issued.Creds != "operator-session-creds" {
		t.Fatalf("delegated mint did not return the operator session: %+v", issued)
	}
	if !fake.closed {
		t.Fatal("issuer connection was not closed by MintSession")
	}
	if !fake.closedAfterMintBody {
		t.Fatal("Close ran before MintSession's body — the operator-session mint must complete first")
	}
}

// TestDelegatedMintTwiceConnectsTwice: each tab-open is its own issuer
// connect+close, no reuse, no pooling (mirrors connectMinter, AC#4).
func TestDelegatedMintTwiceConnectsTwice(t *testing.T) {
	var connects, closes int
	m := newDelegatedMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (issuerClient, error) {
		connects++
		return &countingIssuer{onClose: func() { closes++ }}, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := m.MintSession(context.Background()); err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
	}
	if connects != 2 || closes != 2 {
		t.Fatalf("two mints opened %d issuer connections and closed %d, want 2 and 2 — no reuse", connects, closes)
	}
}

// TestDelegatedMintClosesOnError: a connection that mints with an error is still
// closed before MintSession returns (the defer covers the error path).
func TestDelegatedMintClosesOnError(t *testing.T) {
	closed := false
	m := newDelegatedMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (issuerClient, error) {
		return &countingIssuer{
			onClose: func() { closed = true },
			mintErr: errors.New("delegated mint refused"),
		}, nil
	}
	if _, err := m.MintSession(context.Background()); err == nil {
		t.Fatal("MintSession returned nil error for a refusing delegated mint")
	}
	if !closed {
		t.Fatal("issuer connection not closed on the mint-error path")
	}
}

// countingIssuer is a minimal issuerClient whose hooks let a test observe close
// and inject a mint error.
type countingIssuer struct {
	onClose func()
	mintErr error
}

func (c *countingIssuer) MintOperatorSession(context.Context) (sextant.IssuedClient, error) {
	if c.mintErr != nil {
		return sextant.IssuedClient{}, c.mintErr
	}
	return sextant.IssuedClient{ID: "01OPERATOR", Creds: "operator-session-creds"}, nil
}

func (c *countingIssuer) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

// TestOperatorSessionFlagSelectsDelegatedMinter: --operator-session resolves to
// Options.OperatorSession, and selectMinter then builds the delegatedMinter (the
// managed component's path) — while the default builds a connectMinter (the
// dev/foreground self-mint, unchanged).
func TestOperatorSessionFlagSelectsDelegatedMinter(t *testing.T) {
	parse := func(args []string) Options {
		fs := flag.NewFlagSet("dash", flag.ContinueOnError)
		f := AddFlags(fs)
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		opts, err := f.Resolve()
		if err != nil {
			t.Fatalf("resolve %v: %v", args, err)
		}
		return opts
	}

	withFlag := parse([]string{"--creds", "/tmp/dash.creds", "--operator-session"})
	if !withFlag.OperatorSession {
		t.Fatal("--operator-session did not set Options.OperatorSession")
	}
	if _, ok := selectMinter(withFlag).(*delegatedMinter); !ok {
		t.Fatalf("--operator-session must select the delegatedMinter, got %T", selectMinter(withFlag))
	}

	dflt := parse([]string{"--creds", "/tmp/dash.creds"})
	if dflt.OperatorSession {
		t.Fatal("OperatorSession defaulted true; the dev/foreground path must stay self-mint")
	}
	if _, ok := selectMinter(dflt).(*connectMinter); !ok {
		t.Fatalf("the default must select the connectMinter (clients.session self-mint), got %T", selectMinter(dflt))
	}
}
