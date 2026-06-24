package dashapi_test

import (
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
)

// TestNoConnectAtRest asserts the dashapi Server holds no standing bus presence
// (ADR-0046, TASK-187): constructing the Server (and any request-free serving)
// dispatches ZERO mints, so the connect-per-request minter behind Bus is never
// asked to connect until a browser tab arrives. The fake records every
// MintSession call; the count must be 0 until a POST /api/session lands.
func TestNoConnectAtRest(t *testing.T) {
	bus := &fakeBus{id: "01DASH"}
	_ = dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})

	if got := bus.mintCount(); got != 0 {
		t.Fatalf("constructing the Server dispatched %d mints, want 0 — the dash must hold no bus presence at rest", got)
	}
}
