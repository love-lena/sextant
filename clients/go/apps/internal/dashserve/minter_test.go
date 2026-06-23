package dashserve

import (
	"context"
	"errors"
	"testing"

	"github.com/love-lena/sextant/clients/go/sdk"
)

// fakeSessionClient is the connect double the minter test injects: it records
// that MintSession ran and that Close ran, and the order between them, so the
// test can assert the connection was closed before MintSession returned to the
// caller.
type fakeSessionClient struct {
	closed       bool
	mintReturned bool
	// closedBeforeMintReturn records the invariant under test: Close (run by the
	// minter's defer) must fire after MintSession's body but before MintSession
	// returns to the minter's caller. We observe it by having Close note whether
	// the mint body had already completed.
	closedAfterMintBody bool
}

func (f *fakeSessionClient) MintSession(context.Context) (sextant.IssuedClient, error) {
	f.mintReturned = true
	return sextant.IssuedClient{ID: "01DASH", Creds: "fake-creds"}, nil
}

func (f *fakeSessionClient) Close() error {
	f.closed = true
	f.closedAfterMintBody = f.mintReturned
	return nil
}

// TestMintSessionConnectsAndClosesPerCall asserts the connect-per-request
// contract (AC#2): one connect per MintSession, the credential flows back from
// the connection, and the connection is CLOSED before MintSession returns — even
// though Close is deferred, it runs before the deferred-return completes.
func TestMintSessionConnectsAndClosesPerCall(t *testing.T) {
	var connects int
	fake := &fakeSessionClient{}
	m := newConnectMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (sessionClient, error) {
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
	if issued.Creds != "fake-creds" {
		t.Fatalf("credential not returned from the connection: %+v", issued)
	}
	// The connection must be gone by the time MintSession returns — no standing
	// connection survives the call.
	if !fake.closed {
		t.Fatal("connection was not closed by MintSession")
	}
	if !fake.closedAfterMintBody {
		t.Fatal("Close ran before MintSession's body — the credential mint must complete first")
	}
}

// TestMintSessionTwiceConnectsTwice: each tab-open is its own connect+close, no
// reuse, no pooling (AC#4).
func TestMintSessionTwiceConnectsTwice(t *testing.T) {
	var connects, closes int
	m := newConnectMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (sessionClient, error) {
		connects++
		return &countingClient{onClose: func() { closes++ }}, nil
	}

	for i := 0; i < 2; i++ {
		if _, err := m.MintSession(context.Background()); err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
	}
	if connects != 2 || closes != 2 {
		t.Fatalf("two mints opened %d connections and closed %d, want 2 and 2 — no reuse", connects, closes)
	}
}

// TestMintSessionClosesOnError: a connection that mints with an error is still
// closed before MintSession returns (the defer covers the error path).
func TestMintSessionClosesOnError(t *testing.T) {
	closed := false
	m := newConnectMinter("creds", "url", "conninfo")
	m.connect = func(context.Context) (sessionClient, error) {
		return &countingClient{
			onClose:  func() { closed = true },
			mintErr:  errors.New("mint refused"),
			mintBody: func() {},
		}, nil
	}

	if _, err := m.MintSession(context.Background()); err == nil {
		t.Fatal("MintSession returned nil error for a refusing mint")
	}
	if !closed {
		t.Fatal("connection not closed on the mint-error path")
	}
}

// countingClient is a minimal sessionClient whose hooks let a test observe close
// and inject a mint error.
type countingClient struct {
	onClose  func()
	mintErr  error
	mintBody func()
}

func (c *countingClient) MintSession(context.Context) (sextant.IssuedClient, error) {
	if c.mintBody != nil {
		c.mintBody()
	}
	if c.mintErr != nil {
		return sextant.IssuedClient{}, c.mintErr
	}
	return sextant.IssuedClient{ID: "01DASH", Creds: "fake-creds"}, nil
}

func (c *countingClient) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}
