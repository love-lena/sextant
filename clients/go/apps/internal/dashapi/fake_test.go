package dashapi_test

import (
	"context"
	"sync"

	"github.com/love-lena/sextant/clients/go/sdk"
)

// fakeBus is a test double for the narrowed dashapi.Bus (ADR-0044): the dash's own
// id plus mint-on-behalf (Register). It is the second implementation of the Bus
// interface (the production one is *sextant.Client), so the mint handler is
// exercised without a real bus. Everything the old fake relayed (clients/messages/
// artifacts/publish/subscribe) is gone — the browser calls those over its own bus
// Client now.
type fakeBus struct {
	id string

	registerErr error
	issued      sextant.IssuedClient // what Register returns on success

	mu          sync.Mutex
	registers   []registerCall // captured Register calls
	mintCounter int
}

type registerCall struct {
	displayName string
	kind        string
}

func (f *fakeBus) ID() string { return f.id }

func (f *fakeBus) Register(_ context.Context, displayName, kind string) (sextant.IssuedClient, error) {
	f.mu.Lock()
	f.registers = append(f.registers, registerCall{displayName: displayName, kind: kind})
	f.mintCounter++
	n := f.mintCounter
	f.mu.Unlock()
	if f.registerErr != nil {
		return sextant.IssuedClient{}, f.registerErr
	}
	// A canned issued credential. If the test seeded f.issued, use it; otherwise
	// synthesize a distinct one per call so a test can assert per-tab uniqueness.
	if f.issued.ID != "" || f.issued.Creds != "" {
		return f.issued, nil
	}
	return sextant.IssuedClient{
		ID:    "01MINTEDBROWSER00000000000",
		Creds: mintedCreds(n),
	}, nil
}

func (f *fakeBus) registerCalls() []registerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registerCall(nil), f.registers...)
}

// mintedCreds is a recognisable per-call creds stand-in so a test can assert the
// handler returned the bus's creds verbatim (and that two tabs get distinct ones).
func mintedCreds(n int) string {
	return "-----BEGIN NATS USER JWT-----\nfake-creds-" + string(rune('0'+n)) + "\n------END NATS USER JWT------\n"
}

// fakeError is a simple error with a fixed message, for the mint-failure path.
type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }
