package shipper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// ErrBackpressure is returned by Run when the BoltDB buffer hits the
// configured hard cap and the shipper is in fail-closed mode. The
// caller (cmd/sextant-shipper) treats this as a non-zero exit.
var ErrBackpressure = errors.New("shipper: backpressure hard cap exceeded")

// Shipper is the long-running NATS → ClickHouse mover.
//
// Lifecycle:
//   - New() opens NATS, opens BoltDB, opens ClickHouse, validates config.
//   - Run() registers JetStream consumers, starts per-table flushers,
//     the drain goroutine, and the metrics publisher. Blocks until ctx
//     is canceled OR ErrBackpressure is returned.
//   - Close() drains in-flight batches into the spillover and tears
//     down NATS + ClickHouse + BoltDB. Idempotent.
//
// A Shipper is single-use; do not call Run twice.
type Shipper struct {
	cfg Config

	nc     *nats.Conn
	js     jetstream.JetStream
	chConn driver.Conn
	buf    *spillover

	// pendingBuckets are the per-table in-memory mailboxes that
	// JetStream consumers feed and per-table flushers drain. Each
	// element is a fixed-capacity buffered channel so a slow flusher
	// pushes back on the consumer (which then refuses to ack) rather
	// than letting the heap grow unbounded.
	pendingBuckets map[Table]chan pendingMsg
	consumers      []jetstream.ConsumeContext

	mu      sync.Mutex
	closed  bool
	runOnce sync.Once

	// metric state (atomic; accessed from multiple goroutines).
	writtenSinceLastTick atomic.Int64
	droppedTotal         atomic.Int64
	errorsTotal          atomic.Int64
	lagNanosLast         atomic.Int64

	// shutdownCause records why Run exited (backpressure vs context).
	shutdownCause atomic.Pointer[error]
}

// pendingMsg pairs a Row with the JetStream message it came from. The
// consumer hands the msg in; the flusher acks it after the row has
// either been written to ClickHouse or persisted to the spillover.
//
// fromBuffer is true when the Row was retrieved from BoltDB by the
// drain loop — in that case msg is nil and there is nothing to ack.
type pendingMsg struct {
	row        Row
	msg        jetstream.Msg
	fromBuffer bool
	bufferKey  []byte
}

// New builds a Shipper from cfg and runtime addresses. Both NATS and
// ClickHouse must be reachable; New returns a wrapped error otherwise.
//
// The caller owns Close; New takes ownership of all sub-resources it
// opens (NATS conn, ClickHouse conn, BoltDB) and tears them down in
// Close even if Run was never called.
func New(ctx context.Context, cfg Config) (*Shipper, error) {
	resolved, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	if err := validateAddr("nats.url", resolved.NATS.URL); err != nil {
		return nil, err
	}
	if err := validateAddr("clickhouse.addr", resolved.ClickHouse.Addr); err != nil {
		return nil, err
	}

	// Load operator creds for NATS.
	creds, err := sextantd.ReadOperatorCreds(resolved.NATS.OperatorCreds)
	if err != nil {
		return nil, fmt.Errorf("shipper: %w", err)
	}
	password, err := sextantd.ReadPasswordFile(resolved.ClickHouse.PasswordFile)
	if err != nil {
		return nil, fmt.Errorf("shipper: %w", err)
	}

	nc, err := nats.Connect(resolved.NATS.URL,
		nats.Name("sextant-shipper"),
		nats.UserInfo(creds.User, creds.Password),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500*time.Millisecond),
		nats.ReconnectJitter(100*time.Millisecond, 500*time.Millisecond),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("shipper: nats connect %s: %w", resolved.NATS.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("shipper: jetstream.New: %w", err)
	}

	chConn, err := openClickHouseConn(ctx, resolved, password)
	if err != nil {
		nc.Close()
		return nil, err
	}

	buf, err := openSpillover(resolved.Buffer.Dir)
	if err != nil {
		_ = chConn.Close()
		nc.Close()
		return nil, err
	}

	pending := make(map[Table]chan pendingMsg, len(AllTables()))
	for _, tbl := range AllTables() {
		// Buffer = 2x flush batch size so a slow flusher backs up the
		// consumer rather than ballooning the heap.
		pending[tbl] = make(chan pendingMsg, resolved.Batch.MaxEvents*2)
	}

	return &Shipper{
		cfg:            resolved,
		nc:             nc,
		js:             js,
		chConn:         chConn,
		buf:            buf,
		pendingBuckets: pending,
	}, nil
}

func openClickHouseConn(ctx context.Context, cfg Config, password string) (driver.Conn, error) {
	opts := &chgo.Options{
		Addr: []string{cfg.ClickHouse.Addr},
		Auth: chgo.Auth{
			Database: cfg.ClickHouse.Database,
			Username: cfg.ClickHouse.User,
			Password: password,
		},
		DialTimeout:     5 * time.Second,
		ReadTimeout:     30 * time.Second,
		ConnMaxLifetime: 1 * time.Hour,
		MaxOpenConns:    8,
		MaxIdleConns:    2,
	}
	conn, err := chgo.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("shipper: open clickhouse: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("shipper: ping clickhouse: %w", err)
	}
	return conn, nil
}

// Run starts all per-table flushers, the drain goroutine, the metrics
// publisher, and every JetStream consumer. Blocks until ctx is canceled
// or a fatal error (hard-cap backpressure) occurs. Run is single-use;
// subsequent calls return an error.
func (s *Shipper) Run(ctx context.Context) error {
	called := false
	var runErr error
	s.runOnce.Do(func() {
		called = true
		runErr = s.runInner(ctx)
	})
	if !called {
		return errors.New("shipper: Run already called")
	}
	return runErr
}

func (s *Shipper) runInner(ctx context.Context) error {
	// Internal cancel so any goroutine that detects backpressure can
	// trigger global shutdown.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Hook ctx cancellation through the cause pointer so Run() knows
	// whether it was a graceful shutdown vs backpressure.
	defer func() {
		if errp := s.shutdownCause.Load(); errp != nil && *errp != nil {
			// already recorded
			return
		}
		// graceful: no cause recorded
	}()

	// Start per-table flushers.
	var wg sync.WaitGroup
	for _, tbl := range AllTables() {
		wg.Add(1)
		go func(t Table) {
			defer wg.Done()
			s.flushLoop(runCtx, t, cancel)
		}(tbl)
	}

	// Start drain loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.drainLoop(runCtx)
	}()

	// Start metrics publisher.
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.metricsLoop(runCtx)
	}()

	// Start JetStream consumers.
	if err := s.startConsumers(runCtx); err != nil {
		cancel()
		// Wait briefly for goroutines to notice cancel.
		wg.Wait()
		return fmt.Errorf("shipper: start consumers: %w", err)
	}

	// Block until ctx is canceled (either externally or by our own
	// backpressure path).
	<-runCtx.Done()

	// Stop all consumers — no more new messages enter the pendings.
	for _, c := range s.consumers {
		c.Stop()
	}

	// Close per-table pending channels so the flushers know to drain
	// what's in flight, push the rest into spillover, and exit.
	for _, ch := range s.pendingBuckets {
		close(ch)
	}

	wg.Wait()

	if errp := s.shutdownCause.Load(); errp != nil && *errp != nil {
		return *errp
	}
	if ctx.Err() != nil {
		return nil // graceful: parent canceled
	}
	return nil
}

// Close releases the underlying NATS connection, ClickHouse connection,
// and BoltDB. Safe to call more than once.
func (s *Shipper) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	for _, c := range s.consumers {
		c.Stop()
	}
	var errs []error
	if s.chConn != nil {
		if err := s.chConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("shipper: close clickhouse: %w", err))
		}
	}
	if s.nc != nil {
		s.nc.Close()
	}
	if s.buf != nil {
		if err := s.buf.Close(); err != nil {
			errs = append(errs, fmt.Errorf("shipper: close spillover: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// startConsumers registers one JetStream durable consumer per
// SubjectMapping and wires its callback to the per-table pending
// channel.
func (s *Shipper) startConsumers(ctx context.Context) error {
	for _, m := range DefaultMappings() {
		stream, err := s.js.Stream(ctx, m.Stream)
		if err != nil {
			return fmt.Errorf("get stream %s: %w", m.Stream, err)
		}
		cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
			Durable:           m.Consumer,
			FilterSubject:     m.Subject,
			AckPolicy:         jetstream.AckExplicitPolicy,
			AckWait:           s.cfg.Batch.AckWait.AsDuration(),
			DeliverPolicy:     jetstream.DeliverAllPolicy,
			MaxAckPending:     s.cfg.Batch.MaxEvents * 4,
			InactiveThreshold: 24 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("create consumer %s: %w", m.Consumer, err)
		}
		mapping := m
		consumeCtx, err := cons.Consume(func(msg jetstream.Msg) {
			s.handleConsumed(ctx, mapping, msg)
		})
		if err != nil {
			return fmt.Errorf("start consume %s: %w", m.Consumer, err)
		}
		s.consumers = append(s.consumers, consumeCtx)
	}
	return nil
}

// handleConsumed is the JetStream consumer callback. Decode envelope,
// route to per-table pending channel; if the channel is full and ctx
// hasn't been canceled, NAK so JetStream redelivers later (this is the
// natural backpressure mechanism inside ack window).
func (s *Shipper) handleConsumed(ctx context.Context, m SubjectMapping, msg jetstream.Msg) {
	row, err := DecodeForTable(m.Table, msg.Subject(), msg.Data())
	if err != nil {
		s.errorsTotal.Add(1)
		log.Printf("shipper: decode %s: %v", m.Subject, err)
		// Terminate the message so JetStream stops redelivering a
		// malformed envelope — we already counted the error and a
		// producer-side bug needs human attention.
		if termErr := msg.Term(); termErr != nil {
			log.Printf("shipper: term: %v", termErr)
		}
		return
	}
	select {
	case s.pendingBuckets[m.Table] <- pendingMsg{row: row, msg: msg}:
	case <-ctx.Done():
		// Shutdown is happening — NAK so JetStream redelivers after
		// the restart. Avoids losing the message.
		_ = msg.Nak()
	}
}

// flushLoop drains one per-table pending channel, accumulating rows
// until either batch size or flush interval triggers an INSERT. On
// ClickHouse error the batch falls through to spillover.
//
// When the pending channel is closed (during shutdown) it flushes
// whatever it has left and exits.
func (s *Shipper) flushLoop(ctx context.Context, table Table, cancelAll context.CancelFunc) {
	pending := s.pendingBuckets[table]
	flushInterval := s.cfg.Batch.FlushInterval.AsDuration()
	maxBatch := s.cfg.Batch.MaxEvents

	batch := make([]pendingMsg, 0, maxBatch)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.writeBatch(ctx, table, batch); err != nil {
			log.Printf("shipper: flush %s: %v", table, err)
			s.errorsTotal.Add(1)
			s.checkBackpressure(cancelAll)
		}
		batch = batch[:0]
	}

	for {
		select {
		case msg, ok := <-pending:
			if !ok {
				// Channel closed — final flush.
				flush()
				return
			}
			batch = append(batch, msg)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			// Drain whatever is queued, but bounded so we eventually
			// exit.
			for drained := 0; drained < maxBatch; drained++ {
				select {
				case msg, ok := <-pending:
					if !ok {
						flush()
						return
					}
					batch = append(batch, msg)
				default:
					flush()
					return
				}
			}
			flush()
			return
		}
	}
}

// writeBatch attempts a ClickHouse INSERT for the batch. On success it
// acks every JetStream message in the batch. On failure it pushes the
// rows to BoltDB; if that succeeds the messages are still acked
// (the durable substrate is now the spillover). If BoltDB itself fails,
// the messages are NAK'd so JetStream will redeliver later.
func (s *Shipper) writeBatch(ctx context.Context, table Table, batch []pendingMsg) error {
	rows := make([]Row, 0, len(batch))
	for _, m := range batch {
		rows = append(rows, m.row)
	}

	insertCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := insertRows(insertCtx, s.chConn, table, rows)
	cancel()

	if err != nil {
		// Fall back to spillover. Only spill the *non-buffer* rows; rows
		// already from the buffer stay in the buffer and the drain loop
		// will retry on its own schedule.
		spill := make([]Row, 0, len(rows))
		fromMsgs := make([]pendingMsg, 0, len(batch))
		for _, m := range batch {
			if m.fromBuffer {
				continue
			}
			spill = append(spill, m.row)
			fromMsgs = append(fromMsgs, m)
		}
		if len(spill) > 0 {
			if _, putErr := s.buf.Put(spill); putErr != nil {
				// Could not even spill — NAK so JetStream redelivers.
				for _, m := range fromMsgs {
					if m.msg != nil {
						_ = m.msg.Nak()
					}
				}
				return fmt.Errorf("clickhouse insert failed AND spillover put failed: ch=%w spill=%w", err, putErr)
			}
		}
		// Spilled successfully — ack JetStream so we don't re-receive.
		for _, m := range batch {
			if m.fromBuffer {
				continue
			}
			if m.msg != nil {
				if ackErr := m.msg.Ack(); ackErr != nil {
					log.Printf("shipper: ack after spill: %v", ackErr)
				}
			}
		}
		return fmt.Errorf("clickhouse insert (spilled to buffer): %w", err)
	}

	// Insert succeeded. Ack live messages and delete buffered keys.
	bufKeys := make([][]byte, 0, len(batch))
	var lagSum int64
	now := time.Now().UTC()
	for _, m := range batch {
		s.writtenSinceLastTick.Add(1)
		lagSum += now.Sub(m.row.EnvelopeTs).Nanoseconds()
		if m.fromBuffer {
			bufKeys = append(bufKeys, m.bufferKey)
			continue
		}
		if m.msg != nil {
			if ackErr := m.msg.Ack(); ackErr != nil {
				log.Printf("shipper: ack: %v", ackErr)
			}
		}
	}
	if len(batch) > 0 {
		s.lagNanosLast.Store(lagSum / int64(len(batch)))
	}
	if len(bufKeys) > 0 {
		if err := s.buf.Delete(table, bufKeys); err != nil {
			log.Printf("shipper: drain delete %s: %v", table, err)
		}
	}
	return nil
}

// drainLoop walks each spillover bucket every second and feeds rows back
// into the per-table flusher. It runs continuously so spillover drains
// as fast as ClickHouse can absorb writes.
func (s *Shipper) drainLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, tbl := range AllTables() {
			s.drainOne(ctx, tbl)
		}
	}
}

// drainOne pulls up to MaxEvents keys from the per-table bucket and
// feeds them into the pending channel as buffered rows.
func (s *Shipper) drainOne(ctx context.Context, tbl Table) {
	keys, rows, err := s.buf.PeekBatch(tbl, s.cfg.Batch.MaxEvents)
	if err != nil {
		log.Printf("shipper: drain peek %s: %v", tbl, err)
		s.errorsTotal.Add(1)
		return
	}
	pending := s.pendingBuckets[tbl]
	for i, r := range rows {
		select {
		case pending <- pendingMsg{row: r, fromBuffer: true, bufferKey: keys[i]}:
		case <-ctx.Done():
			return
		}
	}
}

// checkBackpressure inspects the spillover size against the hard cap.
// Triggers fail-closed (or degraded-mode drop-oldest) when over.
//
// In fail-closed mode it records ErrBackpressure as the shutdown cause
// and calls cancelAll. The metrics + audit publish are best-effort: a
// shipper that can't reach NATS to publish the audit envelope still
// exits non-zero.
func (s *Shipper) checkBackpressure(cancelAll context.CancelFunc) {
	size := s.buf.SizeBytes()
	cap := s.cfg.Buffer.HardCapBytes
	if size < cap {
		return
	}
	switch s.cfg.Shipper.DegradedMode {
	case DegradedModeDropOldest:
		dropped, err := s.buf.DropOldest(s.cfg.Batch.MaxEvents)
		if err != nil {
			log.Printf("shipper: drop_oldest: %v", err)
			s.errorsTotal.Add(1)
			return
		}
		s.droppedTotal.Add(int64(dropped))
		s.emitAuditEnvelope("audit.shipper_drop", "shipper.drop", "drop_oldest",
			fmt.Sprintf("dropped %d entries; buffer size %d >= cap %d", dropped, size, cap))
		log.Printf("shipper: degraded_mode dropped %d entries", dropped)
		return
	default:
		// Fail closed.
		s.emitAuditEnvelope("audit.shipper_backpressure", "shipper.backpressure", "fail_closed",
			fmt.Sprintf("buffer size %d >= cap %d", size, cap))
		log.Printf("shipper: hard cap %d hit; failing closed", cap)
		err := fmt.Errorf("%w: %d >= %d", ErrBackpressure, size, cap)
		s.shutdownCause.Store(&err)
		cancelAll()
	}
}

// emitAuditEnvelope publishes a best-effort audit envelope to NATS.
// Drops on error — we are already in a failure path and don't want to
// block shutdown.
func (s *Shipper) emitAuditEnvelope(subject, action, capability, detail string) {
	from := sextantproto.Address{
		Kind: sextantproto.AddressDaemon,
		ID:   "shipper-" + s.cfg.HostID(),
	}
	payload := sextantproto.AuditPayload{
		Actor:              "sextant-shipper",
		Action:             action,
		CapabilityRequired: capability,
		Result:             sextantproto.AuditError,
		Details: map[string]any{
			"detail": detail,
		},
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAudit, from, payload)
	if err != nil {
		log.Printf("shipper: build audit envelope: %v", err)
		return
	}
	raw, err := json.Marshal(env)
	if err != nil {
		log.Printf("shipper: marshal audit envelope: %v", err)
		return
	}
	if err := s.nc.Publish(subject, raw); err != nil {
		log.Printf("shipper: publish %s: %v", subject, err)
		return
	}
	_ = s.nc.Flush()
}

// metricsLoop publishes shipper.* metrics every MetricsInterval.
func (s *Shipper) metricsLoop(ctx context.Context) {
	interval := s.cfg.Shipper.MetricsInterval.AsDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			s.publishMetricsSnapshot(elapsed)
		}
	}
}

// publishMetricsSnapshot emits one envelope per metric.
func (s *Shipper) publishMetricsSnapshot(elapsed time.Duration) {
	written := s.writtenSinceLastTick.Swap(0)
	bufSize := float64(s.buf.SizeBytes())
	lagSeconds := float64(s.lagNanosLast.Load()) / float64(time.Second)
	errors := float64(s.errorsTotal.Load())
	dropped := float64(s.droppedTotal.Load())
	var rate float64
	if elapsed > 0 {
		rate = float64(written) / elapsed.Seconds()
	}

	now := time.Now().UTC().UnixNano()
	subject := "telemetry.metrics.shipper." + s.cfg.HostID()
	from := sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "shipper-" + s.cfg.HostID()}

	metrics := []sextantproto.Metric{
		{
			Timestamp:   now,
			MetricName:  "shipper.lag_seconds",
			MetricType:  sextantproto.MetricGauge,
			ServiceName: s.cfg.Shipper.ServiceName,
			Value:       lagSeconds,
		},
		{
			Timestamp:   now,
			MetricName:  "shipper.buffer_depth_bytes",
			MetricType:  sextantproto.MetricGauge,
			ServiceName: s.cfg.Shipper.ServiceName,
			Value:       bufSize,
		},
		{
			Timestamp:   now,
			MetricName:  "shipper.write_rate_per_sec",
			MetricType:  sextantproto.MetricGauge,
			ServiceName: s.cfg.Shipper.ServiceName,
			Value:       rate,
		},
		{
			Timestamp:   now,
			MetricName:  "shipper.errors_total",
			MetricType:  sextantproto.MetricSum,
			ServiceName: s.cfg.Shipper.ServiceName,
			Value:       errors,
		},
		{
			Timestamp:   now,
			MetricName:  "shipper.dropped_total",
			MetricType:  sextantproto.MetricSum,
			ServiceName: s.cfg.Shipper.ServiceName,
			Value:       dropped,
		},
	}

	for _, m := range metrics {
		env, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetryMetric, from, m)
		if err != nil {
			log.Printf("shipper: build metric envelope: %v", err)
			continue
		}
		raw, err := json.Marshal(env)
		if err != nil {
			log.Printf("shipper: marshal metric envelope: %v", err)
			continue
		}
		if err := s.nc.Publish(subject, raw); err != nil {
			log.Printf("shipper: publish metric %s: %v", m.MetricName, err)
			continue
		}
	}
	_ = s.nc.Flush()
}

// Stats returns a current snapshot of internal counters. Test-only —
// production callers should consume the published metrics.
type Stats struct {
	BufferDepthBytes int64
	ErrorsTotal      int64
	DroppedTotal     int64
	LagNanosLast     int64
}

// Stats returns a snapshot of counters relevant to operators and tests.
func (s *Shipper) Stats() Stats {
	return Stats{
		BufferDepthBytes: s.buf.SizeBytes(),
		ErrorsTotal:      s.errorsTotal.Load(),
		DroppedTotal:     s.droppedTotal.Load(),
		LagNanosLast:     s.lagNanosLast.Load(),
	}
}

// String returns a single-line identifier for logs.
func (s *Shipper) String() string {
	return fmt.Sprintf("shipper(nats=%s ch=%s buffer=%s host=%s)",
		s.cfg.NATS.URL, s.cfg.ClickHouse.Addr, s.cfg.Buffer.Dir, s.cfg.HostID())
}

// LoadOperatorCreds is a tiny convenience for the cmd package. Keeps
// the import surface of cmd/sextant-shipper tight.
func LoadOperatorCreds(path string) (string, string, error) {
	c, err := sextantd.ReadOperatorCreds(path)
	if err != nil {
		return "", "", err
	}
	return c.User, c.Password, nil
}

// ConfigFromFile loads + merges runtime addrs in one call. Used by the
// cmd binary. Keeps signal-handling code in main.go small.
func ConfigFromFile(path, runtimePath string) (Config, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return Config{}, err
	}
	if cfg.NATS.URL == "" || cfg.ClickHouse.Addr == "" {
		merged, err := cfg.MergeRuntime(runtimePath)
		if err != nil {
			return Config{}, err
		}
		cfg = merged
	}
	return cfg, nil
}

// DefaultRuntimePath returns ~/.local/share/sextant/runtime.json.
func DefaultRuntimePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("shipper: home: %w", err)
	}
	return strings.Join([]string{home, ".local", "share", "sextant", "runtime.json"}, string(os.PathSeparator)), nil
}
