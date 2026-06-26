//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/love-lena/sextant/sdk/go"
	"github.com/nats-io/nats.go"
)

// TestPrincipalDesignation is the TASK-54 / ADR-0030 definition-of-done: the bus
// records its one principal in a client-readable, Operator-writable sx key. It
// drives the built `sextant` binary (and a small in-process SDK client for the
// live-observe assertion) through every acceptance criterion:
//
//   - AC#2: `sextant principal set <ulid>` (operator) sets/re-points it;
//     `sextant principal get` reads it.
//   - AC#3: bus bootstrap defaults the principal to the operator's seat.
//   - AC#4: a connected client discovers the current principal AND observes a
//     change without reconnecting.
//   - AC#5: a non-operator (client-tier) attempt to set the principal is DENIED
//     by the bus — proven at the bus, not by the absence of a CLI command.
func TestPrincipalDesignation(t *testing.T) {
	h := newHarness(t)
	h.startBus()

	// AC#3 — bootstrap default: before anyone enrolls, the principal is the
	// operator's seat (the reserved operator identity, the only seat at bootstrap).
	out, code := h.run(nil, "principal", "get", "--store", h.store, "--creds", h.operatorReadCreds(t))
	if code != 0 {
		t.Fatalf("principal get (bootstrap) exited %d: %s", code, out)
	}
	if got := lastLine(out); got != wireapi.OperatorID {
		t.Fatalf("bootstrap principal = %q, want the operator's seat %q", got, wireapi.OperatorID)
	}

	// Enroll a human seat and an agent client (held-mode mints both under the store).
	humanOut, code := h.run(nil, "clients", "register", "human", "--kind", "human", "--store", h.store)
	if code != 0 {
		t.Fatalf("register human exited %d: %s", code, humanOut)
	}
	humanID := mustParseID(t, humanOut, `registered human as (`+ulidPat+`)`)
	humanCreds := filepath.Join(h.store, "human.creds")

	agentOut, code := h.run(nil, "clients", "register", "agent", "--kind", "worker", "--store", h.store)
	if code != 0 {
		t.Fatalf("register agent exited %d: %s", code, agentOut)
	}
	agentID := mustParseID(t, agentOut, `registered agent as (`+ulidPat+`)`)
	agentCreds := filepath.Join(h.store, "agent.creds")

	// AC#2 — operator set/get round-trip: re-point the principal to the human seat.
	if out, code := h.run(nil, "principal", "set", humanID, "--store", h.store); code != 0 {
		t.Fatalf("principal set exited %d: %s", code, out)
	}
	out, code = h.run(nil, "principal", "get", "--store", h.store, "--creds", agentCreds)
	if code != 0 {
		t.Fatalf("principal get (after set) exited %d: %s", code, out)
	}
	if got := lastLine(out); got != humanID {
		t.Fatalf("principal after set = %q, want the human seat %q", got, humanID)
	}

	// AC#4 — a connected client discovers the current principal AND observes a
	// change without reconnecting. Connect an SDK client (the human seat), watch
	// the principal, then re-point it via the CLI and assert the live delivery.
	c, err := sextant.Connect(context.Background(), sextant.Options{
		URL:       busURL(t, h.store),
		CredsPath: humanCreds,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("connect watcher: %v", err)
	}
	defer c.Close()
	// Discover-on-connect: the value is already known from the hello handshake.
	if got := c.Principal(); got != humanID {
		t.Fatalf("discovered-on-connect principal = %q, want %q", got, humanID)
	}

	changes := make(chan string, 8)
	w, err := c.WatchPrincipal(context.Background(), func(p string) { changes <- p })
	if err != nil {
		t.Fatalf("WatchPrincipal: %v", err)
	}
	defer func() { _ = w.Stop() }()
	// First delivery is the current value.
	if got := recv(t, changes); got != humanID {
		t.Fatalf("first watch delivery = %q, want current %q", got, humanID)
	}
	// Re-point to the agent via the CLI — deliberate, so it takes --force
	// (ADR-0031); the still-connected watcher observes it.
	if out, code := h.run(nil, "principal", "set", agentID, "--force", "--store", h.store); code != 0 {
		t.Fatalf("principal re-point exited %d: %s", code, out)
	}
	if got := recv(t, changes); got != agentID {
		t.Fatalf("watch delivery after re-point = %q, want %q", got, agentID)
	}

	// AC#5 — a client-tier set is DENIED by the bus. Publish principal.set raw
	// under the agent's OWN api prefix (which its allow-list permits): the call
	// reaches the bus, and the bus rejects it on the operator-only gate. Proving
	// the gate at the bus is the security-critical point.
	assertClientSetDenied(t, h, agentCreds, agentID, humanID)

	// The denied write changed nothing: still the agent (the last operator set).
	out, code = h.run(nil, "principal", "get", "--store", h.store, "--creds", agentCreds)
	if code != 0 {
		t.Fatalf("principal get (after denied set) exited %d: %s", code, out)
	}
	if got := lastLine(out); got != agentID {
		t.Fatalf("principal after a denied client set = %q, want unchanged %q", got, agentID)
	}
}

// assertClientSetDenied connects raw NATS with a client credential and invokes
// principal.set under that client's own API prefix. The publish is allowed (the
// prefix is the client's own), so the request reaches the bus; the bus rejects it
// with the operator-only error in the Wire API response.
func assertClientSetDenied(t *testing.T, h *harness, clientCreds, clientID, attempt string) {
	t.Helper()
	nc, err := nats.Connect(busURL(t, h.store),
		nats.UserCredentials(clientCreds),
		nats.CustomInboxPrefix(wireapi.InboxPrefix(clientID)))
	if err != nil {
		t.Fatalf("client set probe connect: %v", err)
	}
	defer nc.Close()

	data, err := json.Marshal(wireapi.PrincipalSetInput{Principal: attempt})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := nc.Request(wireapi.CallSubject(clientID, wireapi.OpPrincipalSet), data, 5*time.Second)
	if err != nil {
		t.Fatalf("client set probe request: %v", err)
	}
	var resp wireapi.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("a client-tier principal.set must be DENIED by the bus, but it succeeded")
	}
	if !strings.Contains(resp.Error, "only the operator may re-point an established principal") {
		t.Fatalf("expected the operator-only re-point gate error, got: %s", resp.Error)
	}
}

// operatorReadCreds returns a client credential to READ the principal with. The
// operator credential is not a directory client (it runs no hello handshake), so
// `principal get` reads as a regular client; this mints one for the bootstrap
// read before any other client exists.
func (h *harness) operatorReadCreds(t *testing.T) string {
	t.Helper()
	out, code := h.run(nil, "clients", "register", "reader", "--kind", "client", "--store", h.store)
	if code != 0 {
		t.Fatalf("register reader exited %d: %s", code, out)
	}
	return filepath.Join(h.store, "reader.creds")
}

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(stepTimeout):
		t.Fatalf("no principal delivery within %s", stepTimeout)
		return ""
	}
}

// lastLine returns the last non-empty line of out (the CLI prints the principal
// on its own line; stderr notices, if any, precede it).
func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// principalReader mints a client credential just to READ the principal, and
// returns a closure that runs `principal get` and returns the current value.
func (h *harness) principalReader(t *testing.T) func() string {
	t.Helper()
	creds := h.operatorReadCreds(t)
	return func() string {
		out, code := h.run(nil, "principal", "get", "--store", h.store, "--creds", creds)
		if code != 0 {
			t.Fatalf("principal get exited %d: %s", code, out)
		}
		return lastLine(out)
	}
}

// TestPrincipalClaimOnSelfEnroll is the ADR-0031 definition-of-done driven
// through the built binary: a human self-enroll claims an unclaimed principal
// with no second command; a LATER self-enroll does not (already established);
// and re-pointing an established principal takes --force.
func TestPrincipalClaimOnSelfEnroll(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	principal := h.principalReader(t)

	// Bootstrap: unclaimed (the operator seat).
	if got := principal(); got != wireapi.OperatorID {
		t.Fatalf("bootstrap principal = %q, want %q", got, wireapi.OperatorID)
	}

	// A human self-enroll claims the unclaimed principal — no second command.
	out, code := h.run(map[string]string{"SEXTANT_HOME": t.TempDir(), "USER": "alice"},
		"clients", "register", "--self", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self (alice) exited %d: %s", code, out)
	}
	if !strings.Contains(out, "this seat is now the bus principal") {
		t.Fatalf("a first self-enroll should claim the principal; got: %s", out)
	}
	aliceID := mustParseID(t, out, `enrolled as (`+ulidPat+`)`)
	if got := principal(); got != aliceID {
		t.Fatalf("principal after self-enroll = %q, want the new seat %q", got, aliceID)
	}

	// A LATER self-enroll does not claim — the principal is already established.
	out, code = h.run(map[string]string{"SEXTANT_HOME": t.TempDir(), "USER": "bob"},
		"clients", "register", "--self", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self (bob) exited %d: %s", code, out)
	}
	if strings.Contains(out, "this seat is now the bus principal") {
		t.Fatalf("a second self-enroll must NOT claim an established principal; got: %s", out)
	}
	bobID := mustParseID(t, out, `enrolled as (`+ulidPat+`)`)
	if got := principal(); got != aliceID {
		t.Fatalf("principal after a second self-enroll = %q, want unchanged %q", got, aliceID)
	}

	// Re-pointing an established principal takes --force: without it the CLI fails
	// and the designation is unchanged; with it, the move proceeds.
	if out, code := h.run(nil, "principal", "set", bobID, "--store", h.store); code == 0 {
		t.Fatalf("re-point without --force must fail; got success: %s", out)
	}
	if got := principal(); got != aliceID {
		t.Fatalf("principal after a forceless re-point = %q, want unchanged %q", got, aliceID)
	}
	if out, code := h.run(nil, "principal", "set", bobID, "--force", "--store", h.store); code != 0 {
		t.Fatalf("forced re-point exited %d: %s", code, out)
	}
	if got := principal(); got != bobID {
		t.Fatalf("principal after forced re-point = %q, want %q", got, bobID)
	}
}

// TestPrincipalNoClaimPaths drives the two self-enroll paths that must NOT claim
// the principal even when it is unclaimed: --no-principal (the explicit opt-out)
// and an agent seat (kind=agent — human-only at the source, enforced by the bus).
func TestPrincipalNoClaimPaths(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	principal := h.principalReader(t)

	// --no-principal: a human self-enroll that opts out leaves the principal unclaimed.
	out, code := h.run(map[string]string{"SEXTANT_HOME": t.TempDir(), "USER": "carol"},
		"clients", "register", "--self", "--no-principal", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self --no-principal exited %d: %s", code, out)
	}
	if strings.Contains(out, "this seat is now the bus principal") {
		t.Fatalf("--no-principal must not claim; got: %s", out)
	}
	if got := principal(); got != wireapi.OperatorID {
		t.Fatalf("principal after --no-principal = %q, want unclaimed %q", got, wireapi.OperatorID)
	}

	// An agent self-enroll never claims, even as the first seat: the bus rejects an
	// agent target on the claim path, so the principal stays unclaimed.
	out, code = h.run(map[string]string{"SEXTANT_HOME": t.TempDir(), "USER": "agent-x"},
		"clients", "register", "--self", "--kind", "agent", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self --kind agent exited %d: %s", code, out)
	}
	if strings.Contains(out, "this seat is now the bus principal") {
		t.Fatalf("an agent self-enroll must not claim; got: %s", out)
	}
	if got := principal(); got != wireapi.OperatorID {
		t.Fatalf("principal after agent self-enroll = %q, want unclaimed %q", got, wireapi.OperatorID)
	}
}
