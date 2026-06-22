package dashapi_test

import (
	"context"
	"sync"

	"github.com/love-lena/sextant/clients/go/sdk"
)

// fakeBus is a test double for the narrowed dashapi.Bus (ADR-0044): the one bus
// act the dash needs — MintSession, a short-lived SESSION credential for the
// dash's OWN identity (the operator's). It is the second implementation of the Bus
// interface (the production one is *sextant.Client), so the mint handler is
// exercised without a real bus. Everything the old API relayed (clients/messages/
// artifacts/publish/subscribe) is gone — the browser calls those over its own bus
// Client now.
type fakeBus struct {
	id string // the dash's own id; MintSession issues a session credential for it

	mintErr error                // when set, MintSession fails (the refusal path)
	issued  sextant.IssuedClient // when set, the canned credential MintSession returns

	mu          sync.Mutex
	mintCounter int // session credentials minted (one per tab)
}

// MintSession returns a session credential for the dash's own id. The creds vary
// per call (via the counter) so a test can assert two tabs never share material.
func (f *fakeBus) MintSession(_ context.Context) (sextant.IssuedClient, error) {
	f.mu.Lock()
	f.mintCounter++
	n := f.mintCounter
	f.mu.Unlock()
	if f.mintErr != nil {
		return sextant.IssuedClient{}, f.mintErr
	}
	if f.issued.ID != "" || f.issued.Creds != "" {
		return f.issued, nil
	}
	return sextant.IssuedClient{ID: f.id, Creds: mintedCreds(n)}, nil
}

func (f *fakeBus) mintCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mintCounter
}

// mintedCreds is a recognisable per-call creds stand-in so a test can assert the
// handler returned the bus's creds verbatim (and that two tabs get distinct ones).
func mintedCreds(n int) string {
	return "-----BEGIN NATS USER JWT-----\nfake-creds-" + string(rune('0'+n)) + "\n------END NATS USER JWT------\n"
}

// fakeError is a simple error with a fixed message, for the mint-failure path.
type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }
