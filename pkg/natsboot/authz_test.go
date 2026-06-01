package natsboot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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
	// $JS.ACK.> must be in BOTH non-daemon publish lists: a JetStream
	// consumer acks by publishing there, so without it every operator
	// read-path consumer and the sidecar inbox loop wedge. Guarded
	// server-less here; exercised end-to-end in
	// TestOperatorJetStreamConsumerCanAck.
	if !containsSubject(operatorPublishAllow, "$JS.ACK.>") {
		t.Errorf("operator publish allow-list missing $JS.ACK.> — JetStream consumers cannot ack:\n%v", operatorPublishAllow)
	}
	if !containsSubject(sidecarPublishAllow, "$JS.ACK.>") {
		t.Errorf("sidecar publish allow-list missing $JS.ACK.> — inbox ack wedges:\n%v", sidecarPublishAllow)
	}
}

// containsSubject reports whether subjects contains want.
func containsSubject(subjects []string, want string) bool {
	for _, s := range subjects {
		if s == want {
			return true
		}
	}
	return false
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

// TestOperatorCanReadInboxViaJetStream is the regression guard for "reads
// stay off the gauntlet" (RFC §5.7): even though the operator principal
// may NOT publish to or core-subscribe agents.*.inbox, it CAN read the
// agent_inbox stream through the JetStream API ($JS.API.>) — the path
// pkg/client.Subscribe and the KV-backed read TUIs use. The daemon
// publishes; the operator consumes via an ordered consumer.
func TestOperatorCanReadInboxViaJetStream(t *testing.T) {
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

	// Daemon bootstraps the streams + publishes a prompt onto the inbox.
	dNC, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect (daemon): %v", err)
	}
	defer dNC.Close()
	if err := Bootstrap(ctx, dNC, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	dJS, err := jetstream.New(dNC)
	if err != nil {
		t.Fatalf("daemon jetstream.New: %v", err)
	}
	const inbox = "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.inbox"
	if _, err := dJS.Publish(ctx, inbox, []byte(`{"kind":"prompt","content":"hi"}`)); err != nil {
		t.Fatalf("daemon JS publish inbox: %v", err)
	}

	// Operator consumes the agent_inbox stream via JetStream. This must
	// succeed despite the operator lacking core inbox permissions.
	opNC, err := srv.ConnectOperator()
	if err != nil {
		t.Fatalf("ConnectOperator: %v", err)
	}
	defer opNC.Close()
	opJS, err := jetstream.New(opNC)
	if err != nil {
		t.Fatalf("operator jetstream.New: %v", err)
	}
	consumer, err := opJS.OrderedConsumer(ctx, "agent_inbox", jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{inbox},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("operator ordered consumer on agent_inbox: %v", err)
	}
	msg, err := consumer.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("operator read inbox via JetStream: %v", err)
	}
	if !strings.Contains(string(msg.Data()), "prompt") {
		t.Fatalf("unexpected inbox payload: %s", msg.Data())
	}
}

// TestOperatorJetStreamConsumerCanAck is the regression guard the original
// F0 authz test lacked. Reading via JetStream is not enough: a real consumer
// ACKNOWLEDGES each delivered message, and a JetStream ack is a *publish* to
// the per-message $JS.ACK.<…> reply subject — a distinct subject space from
// the $JS.API.> management surface. Before this fix the operator (and
// sidecar) publish allow-lists granted $JS.API.> but NOT $JS.ACK.>, so every
// non-daemon consumer that acked got "Permissions Violation for Publish to
// $JS.ACK.…" the instant it tried — wedging `agents context`, the frame /
// lifecycle read TUIs, and the sidecar's inbox prompt loop in PRODUCTION,
// not just in the e2e suite.
//
// This stands up a real nats-server, has the daemon publish a prompt, then
// has the OPERATOR principal create an AckExplicit pull consumer, fetch the
// message, and Ack() it — the ack MUST succeed. We assert no async
// permissions violation lands and that DoubleAck (which waits for the
// server's ack-of-the-ack on $JS.ACK) returns nil.
func TestOperatorJetStreamConsumerCanAck(t *testing.T) {
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

	// Daemon bootstraps the streams + publishes onto the frames stream (the
	// read path `agents context` and the frame TUIs consume; the operator
	// consumes + acks it).
	dNC, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect (daemon): %v", err)
	}
	defer dNC.Close()
	if err := Bootstrap(ctx, dNC, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	dJS, err := jetstream.New(dNC)
	if err != nil {
		t.Fatalf("daemon jetstream.New: %v", err)
	}
	const frames = "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.frames"
	if _, err := dJS.Publish(ctx, frames, []byte(`{"kind":"frame","content":"hi"}`)); err != nil {
		t.Fatalf("daemon JS publish frames: %v", err)
	}

	// Operator connects and surfaces async permissions violations on a chan.
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

	opJS, err := jetstream.New(opNC)
	if err != nil {
		t.Fatalf("operator jetstream.New: %v", err)
	}
	// AckExplicit consumer (the same ack discipline pkg/client and the
	// sidecar drive) — its ack publishes to $JS.ACK.>.
	consumer, err := opJS.CreateOrUpdateConsumer(ctx, "agent_frames", jetstream.ConsumerConfig{
		FilterSubjects: []string{frames},
		AckPolicy:      jetstream.AckExplicitPolicy,
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("operator create AckExplicit consumer on agent_frames: %v", err)
	}
	msg, err := consumer.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("operator fetch frame: %v", err)
	}
	// DoubleAck publishes the ack to $JS.ACK.> AND waits for the server's
	// confirmation, so a permissions violation surfaces synchronously here
	// rather than only on the async error handler.
	ackCtx, ackCancel := context.WithTimeout(ctx, 5*time.Second)
	defer ackCancel()
	if err := msg.DoubleAck(ackCtx); err != nil {
		t.Fatalf("operator JetStream ack ($JS.ACK.>) failed — front-door perms gap: %v", err)
	}
	_ = opNC.Flush()

	select {
	case e := <-permErr:
		t.Fatalf("operator ack triggered a permissions violation (missing $JS.ACK.> grant): %v", e)
	case <-time.After(500 * time.Millisecond):
		// No violation — the grant is in place.
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
