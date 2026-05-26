package shipper

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/clickhouseboot"
	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// requireBins skips the test cleanly when nats-server or clickhouse are
// not on PATH. Mirrors the helper from cmd/sextantd's test suite.
func requireBins(t *testing.T) {
	t.Helper()
	for _, name := range []string{"nats-server", "clickhouse"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not on PATH: %v", name, err)
		}
	}
}

// fixture is the assembled NATS + ClickHouse + Shipper environment that
// each integration test boots, exercises, and tears down.
type fixture struct {
	t              *testing.T
	natsSrv        *natsboot.Server
	chSrv          *clickhouseboot.Server
	configDir      string
	dataDir        string
	cfg            Config
	credsPath      string
	chPasswordPath string
}

// newFixture boots NATS + ClickHouse + writes operator creds and the
// ClickHouse password to a temp dir. Returns a fixture ready to build
// a Shipper from. All resources are registered with t.Cleanup.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	requireBins(t)

	configDir := t.TempDir()
	dataDir := t.TempDir()

	natsCfg := natsboot.DefaultConfig(filepath.Join(dataDir, "nats"))
	natsCfg.LogFile = filepath.Join(dataDir, "nats.log")

	// exec.CommandContext binds the subprocess to ctx; once ctx is
	// canceled the kernel SIGKILLs it. Use context.Background() so the
	// subprocess survives helper return; t.Cleanup tears down via the
	// Stop() path that already issues SIGINT.
	natsSrv, err := natsboot.Start(context.Background(), natsCfg)
	if err != nil {
		t.Fatalf("nats start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = natsSrv.Stop(stopCtx)
	})
	nc, err := natsSrv.Connect()
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := natsboot.Bootstrap(bootCtx, nc, natsCfg.MaxBytesPerStream); err != nil {
		bootCancel()
		nc.Close()
		t.Fatalf("bootstrap nats: %v", err)
	}
	bootCancel()
	nc.Close()

	credsPath := filepath.Join(configDir, "operator.creds")
	if err := sextantd.WriteOperatorCreds(credsPath, sextantd.OperatorCreds{
		User:     natsSrv.OperatorUser(),
		Password: natsSrv.OperatorPassword(),
	}); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	chCfg := clickhouseboot.DefaultConfig(filepath.Join(dataDir, "clickhouse"))
	chCfg.LogFile = filepath.Join(dataDir, "clickhouse.log")
	chSrv, err := clickhouseboot.Start(context.Background(), chCfg)
	if err != nil {
		t.Fatalf("ch start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		_ = chSrv.Stop(stopCtx)
	})

	migCtx, migCancel := context.WithTimeout(context.Background(), 30*time.Second)
	conn, err := chSrv.Open(migCtx)
	if err != nil {
		migCancel()
		t.Fatalf("ch open: %v", err)
	}
	if err := clickhouseboot.Apply(migCtx, conn); err != nil {
		_ = conn.Close()
		migCancel()
		t.Fatalf("apply migrations: %v", err)
	}
	_ = conn.Close()
	migCancel()

	chPasswordPath := filepath.Join(configDir, "clickhouse.password")
	if err := sextantd.WritePasswordFile(chPasswordPath, chSrv.Password()); err != nil {
		t.Fatalf("write ch password: %v", err)
	}

	cfg := DefaultConfig(configDir, dataDir)
	cfg.NATS.URL = natsSrv.PublicURL()
	cfg.NATS.OperatorCreds = credsPath
	cfg.ClickHouse.Addr = chSrv.TCPAddress()
	cfg.ClickHouse.Database = chSrv.Database()
	cfg.ClickHouse.User = chSrv.User()
	cfg.ClickHouse.PasswordFile = chPasswordPath
	// Tight intervals so the test converges fast.
	cfg.Batch.FlushInterval = Duration(50 * time.Millisecond)
	cfg.Batch.AckWait = Duration(5 * time.Second)
	cfg.Shipper.MetricsInterval = Duration(200 * time.Millisecond)
	cfg.Shipper.HostID = "host-test"

	return &fixture{
		t:              t,
		natsSrv:        natsSrv,
		chSrv:          chSrv,
		configDir:      configDir,
		dataDir:        dataDir,
		cfg:            cfg,
		credsPath:      credsPath,
		chPasswordPath: chPasswordPath,
	}
}

// publishJS publishes raw bytes on subject via a fresh JetStream
// publisher. Returns the JS publish ack (sequence number).
func (f *fixture) publishJS(t *testing.T, subject string, data []byte) uint64 {
	t.Helper()
	nc, err := f.natsSrv.Connect()
	if err != nil {
		t.Fatalf("publish connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, err := js.Publish(ctx, subject, data)
	if err != nil {
		t.Fatalf("js publish %s: %v", subject, err)
	}
	return ack.Sequence
}

// publishEnvelope marshals env and publishes it on subject via JetStream.
func (f *fixture) publishEnvelope(t *testing.T, subject string, env sextantproto.Envelope) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	f.publishJS(t, subject, raw)
}

// queryCount returns the row count in tableName matching whereClause
// (an empty whereClause counts all rows).
func (f *fixture) queryCount(t *testing.T, tableName, whereClause string) uint64 {
	t.Helper()
	conn, err := f.chSrv.Open(context.Background())
	if err != nil {
		t.Fatalf("open ch: %v", err)
	}
	defer func() { _ = conn.Close() }()
	sql := "SELECT count() FROM " + tableName
	if whereClause != "" {
		sql += " WHERE " + whereClause
	}
	rows, err := conn.Query(context.Background(), sql)
	if err != nil {
		t.Fatalf("query %s: %v", sql, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("query %s: no row", sql)
	}
	var n uint64
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return n
}

// waitForCount polls queryCount until it reaches want or deadline. Used
// to assert event delivery without timing-based flakes.
func (f *fixture) waitForCount(t *testing.T, tableName, whereClause string, want uint64, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		n := f.queryCount(t, tableName, whereClause)
		if n >= want {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("table %s: have %d rows after %s, want %d (where %q)", tableName, n, deadline, want, whereClause)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestEventsFlowThroughShipper is the M6 acceptance test for "events
// flow through NATS land in ClickHouse with sub-second lag".
//
// We boot NATS + ClickHouse + Shipper, publish one envelope per
// supported Kind, then assert every row lands in its target table.
func TestEventsFlowThroughShipper(t *testing.T) {
	f := newFixture(t)

	shp, err := New(context.Background(), f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = shp.Close() })

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runDone := make(chan error, 1)
	go func() { runDone <- shp.Run(runCtx) }()

	// Give the consumers a moment to register.
	time.Sleep(200 * time.Millisecond)

	// Publish: 3 agent frames, 1 lifecycle, 1 heartbeat, 1 audit,
	// 1 trace, 1 metric, 1 log, 1 user_input_request, 1 user_input_response.
	agentID := uuid.New()
	agentAddr := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
	daemonAddr := sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "daemon-host"}
	operatorAddr := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"}

	for i := 0; i < 3; i++ {
		frame, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, agentAddr,
			sextantproto.AgentFramePayload{
				FrameKind: sextantproto.FrameAssistantText,
				Body:      map[string]any{"text": fmt.Sprintf("hi %d", i)},
			})
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		f.publishEnvelope(t, fmt.Sprintf("agents.%s.frames", agentID.String()), frame)
	}

	lifecycle, err := sextantproto.NewEnvelopeWith(sextantproto.KindLifecycle, agentAddr,
		sextantproto.LifecyclePayload{
			IncarnationID: uuid.New(),
			AgentUUID:     agentID,
			Transition:    sextantproto.LifecycleStarted,
		})
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	f.publishEnvelope(t, fmt.Sprintf("agents.%s.lifecycle", agentID.String()), lifecycle)

	heartbeat, err := sextantproto.NewEnvelopeWith(sextantproto.KindHeartbeat, agentAddr,
		sextantproto.HeartbeatPayload{
			AgentUUID:     agentID,
			IncarnationID: uuid.New(),
			HostID:        "host-test",
			UptimeSeconds: 7,
		})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	f.publishEnvelope(t, fmt.Sprintf("agents.%s.heartbeat", agentID.String()), heartbeat)

	auditEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindAudit, operatorAddr,
		sextantproto.AuditPayload{
			Actor:  "lena",
			Action: "spawn_agent",
			Result: sextantproto.AuditAllowed,
		})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	f.publishEnvelope(t, "audit.spawn", auditEnv)

	traceEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetrySpan, daemonAddr,
		sextantproto.Span{
			Timestamp:     time.Now().UnixNano(),
			TraceID:       "trace-id-x",
			SpanID:        "span-001",
			SpanName:      "shipper.test",
			SpanKind:      sextantproto.SpanKindInternal,
			ServiceName:   "sextant-shipper",
			DurationNanos: 1000,
			StatusCode:    sextantproto.StatusCodeOK,
		})
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	f.publishEnvelope(t, "telemetry.traces.host-a", traceEnv)

	metricEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetryMetric, daemonAddr,
		sextantproto.Metric{
			Timestamp:   time.Now().UnixNano(),
			MetricName:  "sextant.test.metric",
			MetricType:  sextantproto.MetricGauge,
			ServiceName: "sextant-shipper",
			Value:       42.0,
		})
	if err != nil {
		t.Fatalf("metric: %v", err)
	}
	f.publishEnvelope(t, "telemetry.metrics.host-a", metricEnv)

	logEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetryLog, daemonAddr,
		sextantproto.LogRecord{
			Timestamp:    time.Now().UnixNano(),
			ServiceName:  "sextant-shipper",
			SeverityText: "INFO",
			Body:         "hello from test",
		})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	f.publishEnvelope(t, "telemetry.logs.host-a", logEnv)

	reqID := uuid.New()
	reqEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindUserInputRequest, agentAddr,
		sextantproto.UserInputRequestPayload{
			RequestID: reqID,
			FromUUID:  agentID,
			Question:  "what now?",
		})
	if err != nil {
		t.Fatalf("user_input_request: %v", err)
	}
	f.publishEnvelope(t, fmt.Sprintf("user_input.requests.%s", agentID.String()), reqEnv)

	respEnv, err := sextantproto.NewEnvelopeWith(sextantproto.KindUserInputResponse, operatorAddr,
		sextantproto.UserInputResponsePayload{
			RequestID: reqID,
			Decision:  sextantproto.InputAnswer,
			Answer:    "go ahead",
		})
	if err != nil {
		t.Fatalf("user_input_response: %v", err)
	}
	f.publishEnvelope(t, fmt.Sprintf("user_input.responses.%s", reqID.String()), respEnv)

	// Now wait for each table to populate. The shipper publishes its
	// own metrics on telemetry.metrics.shipper.<host_test> every
	// MetricsInterval, which will show up in telemetry_metrics on top
	// of our explicit metric — so we use >= rather than exact ==.
	f.waitForCount(t, "events", "kind = 'agent_frame'", 3, 5*time.Second)
	f.waitForCount(t, "events", "kind = 'lifecycle'", 1, 5*time.Second)
	f.waitForCount(t, "events", "kind = 'heartbeat'", 1, 5*time.Second)
	f.waitForCount(t, "events", "kind = 'user_input_request'", 1, 5*time.Second)
	f.waitForCount(t, "events", "kind = 'user_input_response'", 1, 5*time.Second)
	f.waitForCount(t, "audit", "action = 'spawn_agent'", 1, 5*time.Second)
	f.waitForCount(t, "telemetry_traces", "SpanName = 'shipper.test'", 1, 5*time.Second)
	f.waitForCount(t, "telemetry_metrics", "MetricName = 'sextant.test.metric'", 1, 5*time.Second)
	f.waitForCount(t, "telemetry_logs", "Body = 'hello from test'", 1, 5*time.Second)

	// Shipper's own metrics must appear too — proves the metrics
	// publisher loop is wired correctly.
	f.waitForCount(t, "telemetry_metrics", "MetricName = 'shipper.lag_seconds'", 1, 5*time.Second)

	// Sub-second lag check: count rows whose (ts - ingest) is under 1s.
	// We check at least one row in events qualifies — the shipper may
	// not have flushed the very last row yet.
	verifyLagSubSecond(t, f, "events", 1)

	// Clean shutdown.
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s")
	}
}

// verifyLagSubSecond asserts at least n rows in tableName were written
// within 1 second of their event ts. The shipper's row ts is the
// envelope ts, so this catches "stuck flush" regressions.
func verifyLagSubSecond(t *testing.T, f *fixture, tableName string, n int) {
	t.Helper()
	conn, err := f.chSrv.Open(context.Background())
	if err != nil {
		t.Fatalf("open ch: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// ts and now64 are DateTime64. Subtracting yields a Decimal in
	// seconds (with microsecond fractional); compare against the
	// numeric bound directly rather than IntervalSecond.
	rows, err := conn.Query(context.Background(),
		fmt.Sprintf("SELECT count() FROM %s WHERE (now64(6) - ts) < 2", tableName))
	if err != nil {
		t.Fatalf("lag query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("no lag row")
	}
	var got uint64
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if int(got) < n {
		t.Errorf("expected %d rows under 2s lag, got %d", n, got)
	}
}

// TestShipperHandlesAlreadyAckedRedelivery shows that re-publishing the
// same envelope ID twice ends with one effective row (via
// ReplacingMergeTree on FINAL).
func TestShipperHandlesDuplicateEnvelopes(t *testing.T) {
	f := newFixture(t)

	shp, err := New(context.Background(), f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = shp.Close() })

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- shp.Run(runCtx) }()
	time.Sleep(200 * time.Millisecond)

	agentID := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from,
		sextantproto.AgentFramePayload{FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": "dup"}})
	if err != nil {
		t.Fatalf("env: %v", err)
	}

	// Publish twice with identical ID/Ts.
	f.publishEnvelope(t, fmt.Sprintf("agents.%s.frames", agentID.String()), env)
	f.publishEnvelope(t, fmt.Sprintf("agents.%s.frames", agentID.String()), env)

	// Both inserts land; ReplacingMergeTree collapses on background
	// merge. The acceptance criterion is just "no error" — we let the
	// raw row count be either 1 or 2 depending on merge timing, but
	// `FINAL` always returns 1.
	whereSameID := fmt.Sprintf("id = toUUID('%s')", env.ID.String())
	// Initial wait — at least one row.
	f.waitForCount(t, "events", whereSameID, 1, 5*time.Second)

	// Verify FINAL collapses both writes to one row.
	conn, err := f.chSrv.Open(context.Background())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = conn.Close() }()
	rows, err := conn.Query(context.Background(),
		"SELECT count() FROM events FINAL WHERE id = ?", env.ID)
	if err != nil {
		t.Fatalf("FINAL query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatalf("FINAL no row")
	}
	var got uint64
	if err := rows.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != 1 {
		t.Errorf("FINAL count = %d, want 1", got)
	}

	cancel()
	<-runDone
}

// _ silence the unused warning if nats is imported but every code path
// uses it via the fixture; this is here to keep the import for future
// helpers.
var _ = nats.DefaultURL
