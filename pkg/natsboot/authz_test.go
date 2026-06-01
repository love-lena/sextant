package natsboot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestRenderConfigHasThreeScopedPrincipals is a fast, server-less guard on
// the rendered authorization block: the front door (RFC §5.7) must emit
// exactly the daemon / operator / sidecar users with their scoped allow
// lists, and the operator must NOT be granted publish to agent inboxes.
func TestRenderConfigHasThreeScopedPrincipals(t *testing.T) {
	cfg, err := DefaultConfig(t.TempDir()).validateAndFill()
	if err != nil {
		t.Fatalf("validateAndFill: %v", err)
	}
	var sb strings.Builder
	if err := renderConfig(&sb, cfg); err != nil {
		t.Fatalf("renderConfig: %v", err)
	}
	out := sb.String()

	for _, user := range []string{"daemon", "operator", "sidecar"} {
		if !strings.Contains(out, "user: \""+user+"\"") {
			t.Errorf("rendered config missing user %q:\n%s", user, out)
		}
	}
	// The daemon is the sole publisher: it gets ">".
	if !strings.Contains(out, "publish: { allow: [\">\"] }") {
		t.Errorf("daemon should have publish allow \">\":\n%s", out)
	}
	// The operator front door: it may publish sextant.rpc.* but the
	// operator-publish allow-list must NOT include agents.*.inbox. We pin
	// the package-level allow-lists directly so the rendered-string check
	// can't drift.
	if !strings.Contains(out, "\"sextant.rpc.*\"") {
		t.Errorf("operator should be allowed to publish sextant.rpc.*:\n%s", out)
	}
	for _, s := range operatorPublishAllow {
		if s == "agents.*.inbox" || s == ">" {
			t.Fatalf("operator publish allow-list grants %q — front door is open", s)
		}
	}
}

// TestBrokerRejectsOperatorInboxPublish is the F0 acceptance test: a
// NON-DAEMON credential (the broker-scoped operator principal) attempting
// to publish to agents.<uuid>.inbox is REJECTED BY THE BROKER, while the
// daemon principal succeeds. Real nats-server required; skipped when it is
// not on PATH so the package still builds in a server-less CI lane.
func TestBrokerRejectsOperatorInboxPublish(t *testing.T) {
	bin := natsServerPath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(dir + "/nats")
	cfg.NATSBinary = bin
	cfg.LogFile = dir + "/nats.log"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	const inbox = "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.inbox"

	// Operator principal: the broker must refuse the inbox publish. NATS
	// reports a permissions violation asynchronously, so we wrap the conn
	// with an error callback and flush; the violation surfaces either on
	// the async error channel or as a closed connection.
	opNC, err := srv.ConnectOperator()
	if err != nil {
		t.Fatalf("ConnectOperator: %v", err)
	}
	defer opNC.Close()

	permErr := make(chan error, 1)
	opNC.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		select {
		case permErr <- e:
		default:
		}
	})

	if err := opNC.Publish(inbox, []byte(`{"kind":"prompt"}`)); err != nil {
		// Some clients surface the violation synchronously.
		if !isPermissionsViolation(err) {
			t.Fatalf("operator publish returned unexpected error: %v", err)
		}
		return
	}
	_ = opNC.Flush()

	select {
	case e := <-permErr:
		if !isPermissionsViolation(e) {
			t.Fatalf("operator inbox publish: got error %v, want a permissions violation", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("operator inbox publish was NOT rejected by the broker (front door open)")
	}
}

// TestBrokerAllowsDaemonInboxPublish confirms the daemon principal — the
// sanctioned sole publisher — can still publish to an agent inbox.
func TestBrokerAllowsDaemonInboxPublish(t *testing.T) {
	bin := natsServerPath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(dir + "/nats")
	cfg.NATSBinary = bin
	cfg.LogFile = dir + "/nats.log"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	const inbox = "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.inbox"

	// Daemon principal subscribes to + publishes the inbox (the daemon may
	// do both). Round-trip confirms the grant.
	dNC, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect (daemon): %v", err)
	}
	defer dNC.Close()

	got := make(chan struct{}, 1)
	sub, err := dNC.Subscribe(inbox, func(_ *nats.Msg) {
		select {
		case got <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("daemon subscribe inbox: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck // test cleanup

	if err := dNC.Publish(inbox, []byte(`{"kind":"prompt"}`)); err != nil {
		t.Fatalf("daemon publish inbox: %v", err)
	}
	_ = dNC.Flush()

	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon inbox publish did not round-trip; the sole-publisher grant is broken")
	}
}

// isPermissionsViolation reports whether err is a NATS authorization /
// permissions error (the broker's "not allowed to publish" signal).
func isPermissionsViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission") || strings.Contains(msg, "authorization")
}
