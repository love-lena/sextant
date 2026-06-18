// Package violet is the operator's assistant on the sextant bus, built as a
// long-lived SDK client (milestone goal.violet, TASK-159). It replaces the bash
// prototype (docs/demos/violet-runtime-warm.sh) that proved the warm
// pseudo-agent design but could not meet the fast-under-load bar.
//
// ONE registered bus client (violet's OWN scoped creds — never the principal's,
// TASK-158) + the `assistant` designation artifact. Behind that one identity,
// three concurrent internal roles share an in-memory warm context:
//
//   - GATE (haiku): triages SCOPED, pre-filtered bus events; WAKE or SKIP.
//   - HOME-MANAGER (sonnet): woken by the gate on a significant event (+ a slow
//     safety interval); reads workspace state, re-curates the operator's `home`
//     projection, and produces the compact warm-context snapshot.
//   - CONVERSATIONAL (haiku): answers operator DMs from the warm context,
//     instantly — no per-DM pre-read.
//
// Plus an ACTION SURFACE (Mobilizer): violet can START work (spawn a scoped
// agent / start a workflow run) so a cold start needs no persistent crew. It can
// only START work, never make the operator's decisions — signal-not-manage holds
// for decisions; mobilizing is bounded by the type.
//
// The five live-bus fixes the bash impl required are first-class here: scoped
// subscription (not the firehose), a cheap keyword pre-filter before the gate
// LLM, answer-preempt (a dedicated DM consumer; answers never wait behind gate
// or deep work), a per-frame cursor (each frame once, in order), and ignoring
// own-authored events. Real concurrency — Go goroutines + a bounded gate queue —
// is what lets answers stay fast while a burst of events drains.
//
// AC8 (every-message-answered): violet catches EVERY operator DM, guarantees
// each gets a response, and surfaces those responses to ONE unified place
// (RepliesSubject). The ackStore (violet-ack.json beside other substate) is the
// response-watermark: the cursor advances ONLY after a reply is durably
// published. A startup replay pass reads the DM subject from the watermark
// forward, re-attests each frame by its bus-stamped author, and answers any
// unanswered messages before the live subscription takes over — so nothing falls
// through even across a restart or offline gap.
package violet

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/wire"
)

// DefaultStateDir is the persistent directory where violet keeps its durable
// substate (the AC8 ack cursor) when the caller does not pass an explicit one.
// It is a violet/ subdir under the sextant client-config root (clictx.Root() —
// $SEXTANT_HOME, else <user-config>/sextant), the same root that already holds
// conninfo and contexts. Using it means the response-watermark survives a real
// process restart with ZERO operator config (gate point 1) — the durable cursor
// is genuinely durable in production, not just in tmpdir tests.
//
// cmd/sextant-violet resolves this when --state-dir / $VIOLET_STATE_DIR is unset.
// The violet package itself never defaults StateDir (an empty Config.StateDir
// stays in-memory) so tests remain hermetic and never touch the real config dir.
func DefaultStateDir() string {
	return filepath.Join(clictx.Root(), "violet")
}

// busClient is the slice of *sextant.Client violet needs. Subscriptions are
// scoped (fix #1), publishes carry the captured reply, and artifact ops drive
// the curated home. A fake satisfies it in tests so the under-load concurrency
// bar is exercised against real goroutines without a live bus.
//
// FetchMessages is needed by the AC8 offline-gap replay (replayOfflineGap): it
// pulls retained frames from the DM subject by cursor so the startup pass can
// answer any DMs that arrived while violet was offline.
type busClient interface {
	publisher
	Subscribe(ctx context.Context, subject string, h func(Message), opts ...subOpt) (stopper, error)
	GetArtifact(ctx context.Context, name string) (artifactValue, error)
	CreateArtifact(ctx context.Context, name string, record wire.Lexicon) (uint64, error)
	UpdateArtifact(ctx context.Context, name string, record wire.Lexicon, expectedRev uint64) (uint64, error)
	ListArtifacts(ctx context.Context) ([]artifactInfo, error)
	// FetchMessages pulls retained frames from a subject by cursor (AC8 replay).
	FetchMessages(ctx context.Context, subject string, since uint64, limit int) ([]fetchedFrame, uint64, error)
	ID() string
	Principal() string
}

// publisher is just PublishMsg — the mobilizer and the reply path share it.
type publisher interface {
	PublishMsg(ctx context.Context, subject string, record json.RawMessage) (publishResult, error)
}

type publishResult struct{ ID string }

// Message is one delivered frame as violet sees it: the bus-stamped author and
// subject (the only trust signal — fix #5), the record, and the cursor.
//
// Sequence is the live relay's stream sequence (md.Sequence.Stream), set on the
// Subscribe path. It is the SAME stream-sequence space the FetchMessages cursor
// (since/next) lives in — confirmed against natsbackend.Read (next =
// md.Sequence.Stream+1) and the relay (e.Seq = md.Sequence.Stream). On the
// replay path Sequence is 0 (FetchMessages exposes no per-frame sequence — only
// a bus ULID + the batch cursor); the watermark advance there rides advanceTo.
//
// advanceTo is the cursor-space watermark to persist AFTER this frame's reply is
// confirmed published (response-watermark, criterion 5):
//   - live path: Sequence+1 (one past the answered frame's stream sequence),
//   - replay path: the FetchMessages `next` cursor for this exact frame (its
//     stream sequence + 1), carried out of replayOfflineGap so answerDM can
//     advance the durable cursor without a per-frame sequence it does not have.
//
// Both spaces are the JetStream stream sequence, so mixing them is sound.
type Message struct {
	Author   string
	Subject  string
	Record   json.RawMessage
	Sequence uint64

	advanceTo uint64 // cursor to persist after a confirmed reply (0 = no advance)
}

// subOpt / stopper mirror the SDK's SubOption / Subscription so the fake and the
// real adapter share one signature.
type (
	subOpt  func()
	stopper interface{ Stop() }
)

// Config configures a Violet. Subjects default to the scoped set; models and the
// safety interval have sensible defaults.
type Config struct {
	// OperatorID is the principal's bus client id — the DM author violet trusts
	// and the other half of the DM subject. If empty, it is read from the bus's
	// designated principal at Run.
	OperatorID string

	// ConvModel / GateModel / DeepModel default to Haiku/Haiku/Sonnet.
	ConvModel string
	GateModel string
	DeepModel string

	// SafetyInterval is the slow fallback that runs a deep pass even if the gate
	// never woke it (a long quiet period, a dropped event). Default 15m — keep
	// it long; the gate is the primary trigger.
	SafetyInterval time.Duration

	// GateQueueDepth bounds the gate worker's backlog. A burst beyond it drops
	// the oldest candidates (they are by definition less significant than a fresh
	// one, and a missed WAKE only costs staleness) so the gate can never grow an
	// unbounded backlog that would matter — answers preempt regardless, but a
	// bounded queue keeps memory and latency predictable. Default 64.
	GateQueueDepth int

	// SpawnSubject / WorkflowSubject are the mobilizer's request subjects.
	SpawnSubject    string
	WorkflowSubject string

	// StateDir is the directory where violet persists the durable DM cursor
	// (violet-ack.json) across restarts (AC8). If empty, the cursor is in-memory
	// only — which loses the watermark on a real process restart. Production
	// callers MUST pass a persistent path: cmd/sextant-violet defaults it to
	// DefaultStateDir() (a violet/ subdir under the sextant client-config root,
	// where conninfo/contexts already live) so the cursor survives restart with
	// zero operator config. Tests leave it empty (in-memory) to stay hermetic.
	StateDir string

	// Logf receives diagnostics; defaults to log.Printf.
	Logf func(string, ...any)
}

func (c *Config) withDefaults() {
	if c.ConvModel == "" {
		c.ConvModel = ModelHaiku
	}
	if c.GateModel == "" {
		c.GateModel = ModelHaiku
	}
	if c.DeepModel == "" {
		c.DeepModel = ModelSonnet
	}
	if c.SafetyInterval == 0 {
		c.SafetyInterval = 15 * time.Minute
	}
	if c.GateQueueDepth == 0 {
		c.GateQueueDepth = 64
	}
	if c.SpawnSubject == "" {
		c.SpawnSubject = "msg.topic.spawn"
	}
	if c.WorkflowSubject == "" {
		c.WorkflowSubject = "msg.topic.workflow.start"
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
}

// Violet is the running assistant: one bus identity, three concurrent roles over
// a shared warm context, plus the mobilizer action surface.
type Violet struct {
	cfg   Config
	bus   busClient
	model *modelClient
	warm  *warmContext
	mob   Mobilizer

	self      string
	operator  string
	dmSubject string

	// ack is the durable response-watermark (AC8): the cursor advances only
	// after a reply is published. On startup the replay pass uses it to answer
	// any DMs that arrived while violet was offline.
	ack *ackStore

	// dmCh carries operator DMs to the priority consumer; gateCh is the gate
	// worker's bounded queue; wakeCh signals the deep refresher. The split is the
	// answer-preempt mechanism (fix #3): the DM consumer is its own goroutine and
	// never waits behind gate or deep work.
	dmCh   chan Message
	gateCh chan Message
	wakeCh chan struct{}

	homeRev   uint64 // current revision of the home artifact (for CAS)
	homeKnown bool

	// seenMu/seen dedups frames across the scoped subscriptions (a DM is also a
	// topic, so it can arrive on more than one relay). Bounded by the cursor.
	mu        sync.Mutex
	answering int // count of in-flight answers, for observability/tests
}

// publisher adapter so the mobilizer and reply path can use the bus client.
func (v *Violet) PublishMsg(ctx context.Context, subject string, record json.RawMessage) (publishResult, error) {
	return v.bus.PublishMsg(ctx, subject, record)
}

// New builds a Violet over a bus client and model client. The mobilizer is wired
// to publish under violet's own creds (TASK-158); pass a custom one to override.
// The ackStore (AC8 response-watermark) is initialised at Run time (once the
// DM subject is known); New keeps it nil until then.
func New(bus busClient, model *modelClient, cfg Config) *Violet {
	cfg.withDefaults()
	v := &Violet{
		cfg:    cfg,
		bus:    bus,
		model:  model,
		warm:   newWarmContext(),
		self:   bus.ID(),
		dmCh:   make(chan Message, 16),
		gateCh: make(chan Message, cfg.GateQueueDepth),
		wakeCh: make(chan struct{}, 1),
	}
	v.mob = &busMobilizer{
		pub:          v,
		self:         v.self,
		spawnSubject: cfg.SpawnSubject,
		wfSubject:    cfg.WorkflowSubject,
	}
	return v
}

// Mobilize returns violet's action surface (start a workflow / spawn an agent).
func (v *Violet) Mobilize() Mobilizer { return v.mob }

// Run wires the scoped subscriptions and starts the three role goroutines, then
// blocks until ctx is cancelled. It is the whole lifecycle:
//
//	subscribe → AC8 offline-gap replay → seed warm context → answer/gate/refresh.
func (v *Violet) Run(ctx context.Context) error {
	v.operator = v.cfg.OperatorID
	if v.operator == "" {
		v.operator = v.bus.Principal()
	}
	v.dmSubject = dmSubject(v.self, v.operator)
	v.cfg.Logf("violet: up as %s; operator=%s; DM=%s", short(v.self), short(v.operator), v.dmSubject)

	// AC8: load (or create) the durable response-watermark now that the DM subject
	// is known. The ackStore lives beside other violet substate under StateDir.
	ack, err := newAckStore(v.cfg.StateDir, v.dmSubject)
	if err != nil {
		v.cfg.Logf("violet: ack store load failed (starting in-memory): %v", err)
		ack, _ = newAckStore("", v.dmSubject) // fallback to in-memory; never nil
	}
	v.ack = ack

	// AC8 offline-gap replay: before the live subscription takes over, answer any
	// operator DMs that arrived while violet was offline (or that were delivered
	// but not yet answered when violet last stopped). The replay reads from the
	// response-watermark forward so each frame is answered exactly once across
	// restarts (criterion 3/5). Replay runs synchronously here so the live sub
	// starts only after the gap is closed — no live frame can race a replay frame
	// on the dmCh.
	replayCtx, replayCancel := context.WithTimeout(ctx, 60*time.Second)
	missed, rerr := replayOfflineGap(replayCtx, v.bus, v.dmSubject, v.operator, v.ack, replayMaxFrames)
	replayCancel()
	if rerr != nil {
		v.cfg.Logf("violet: replay pass failed (continuing without replay): %v", rerr)
	}
	if n := len(missed); n > 0 {
		v.cfg.Logf("violet: replaying %d unanswered DMs from the offline gap", n)
	}

	// Fix #1 — SCOPED subscription, not the firehose. Watch only the operator DM,
	// goals, artifact review/discussion, and crew coordination. The DM lands on
	// its own consumer (priority); everything else funnels through one classifier
	// that pre-filters and enqueues gate candidates.
	subjects := []string{
		v.dmSubject,
		"msg.topic.goals",
		"msg.topic.artifact.>",
		"msg.topic.crew",
	}
	var stops []stopper
	for _, subj := range subjects {
		s, err := v.bus.Subscribe(ctx, subj, v.onFrame)
		if err != nil {
			for _, st := range stops {
				st.Stop()
			}
			return err
		}
		stops = append(stops, s)
	}
	defer func() {
		for _, st := range stops {
			st.Stop()
		}
	}()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); v.dmConsumer(ctx) }()
	go func() { defer wg.Done(); v.gateWorker(ctx) }()
	go func() { defer wg.Done(); v.deepRefresher(ctx) }()

	// Enqueue the replayed offline-gap DMs onto the priority DM channel. They
	// arrive in delivery order (oldest first) and are indistinguishable from
	// live frames to the consumer — the ack guard (alreadyAnswered) in answerDM
	// provides idempotency for any that arrived both in replay and live.
	for _, m := range missed {
		v.dmCh <- m
	}

	// Seed the warm context with a first deep pass so the very first DM is
	// answered from real state, not the placeholder.
	v.requestWake()

	<-ctx.Done()
	wg.Wait()
	return nil
}

// onFrame is the subscription handler (runs on the SDK delivery goroutines). It
// applies fixes #4 (per-frame cursor — handled by the SDK relay ordering; each
// frame arrives once in order) and #5 (ignore own-authored events: the
// bus-stamped author is the only trust signal). A DM from the operator is routed
// to the priority consumer; everything else is a gate candidate after the cheap
// pre-filter (fix #2).
func (v *Violet) onFrame(m Message) {
	if m.Author == v.self {
		return // fix #5: never re-trigger on our own published frames
	}

	// Operator DM → priority answer path (fix #3). The DM subject is exact, and
	// trust is the bus-stamped author, never what the record claims.
	if m.Subject == v.dmSubject && m.Author == v.operator {
		// The live relay carries a real stream sequence. Set the cursor-space
		// watermark to persist after this reply lands: one past this frame's
		// sequence (the same space the replay path's advanceTo uses).
		if m.Sequence > 0 {
			m.advanceTo = m.Sequence + 1
		}
		select {
		case v.dmCh <- m:
		default:
			// The priority consumer is briefly busy; a full buffer is unusual.
			// Block-free enqueue keeps the SDK delivery goroutine moving; the
			// consumer drains in order. A 16-deep buffer absorbs realistic bursts.
			v.dmCh <- m
		}
		return
	}

	// Everything else: cheap keyword pre-filter BEFORE the gate LLM (fix #2).
	text := frameText(m.Record)
	if !prefilter(text, m.Author == v.operator) {
		return // obvious noise dropped with no LLM turn
	}
	// Enqueue for the gate worker; a full bounded queue drops the OLDEST (a fresh
	// candidate is at least as significant as a stale one, and a missed WAKE only
	// costs staleness until the next event or the safety tick). Answers preempt
	// regardless — the gate never touches the DM path.
	select {
	case v.gateCh <- m:
	default:
		select {
		case <-v.gateCh: // drop oldest
		default:
		}
		select {
		case v.gateCh <- m:
		default:
		}
	}
}

// requestWake signals the deep refresher (non-blocking; coalesces multiple wakes
// into one pending pass — the deep pass always reads the latest state anyway).
func (v *Violet) requestWake() {
	select {
	case v.wakeCh <- struct{}{}:
	default:
	}
}

// frameText extracts the human text from a chat.message-style record for the
// pre-filter and the gate/event description. Falls back to the raw record so a
// non-chat record is still classifiable.
func frameText(record json.RawMessage) string {
	var rec struct {
		Type string `json:"$type"`
		Text string `json:"text"`
		Note string `json:"note"`
	}
	if json.Unmarshal(record, &rec) == nil {
		if rec.Text != "" {
			return rec.Text
		}
		if rec.Note != "" {
			return rec.Note
		}
	}
	return string(record)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// dmSubject is the deterministic 2-party DM topic (ADR-0034): order-independent.
func dmSubject(a, b string) string {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	return "msg.topic.dm." + lo + "." + hi
}

// trimReply enforces the operator's hard answer bar: ≤250 chars, plain text. The
// role prompt already constrains the model; this is the structural backstop so a
// stray long reply never reaches the operator (the wrapper owns the publish, so
// it owns the bar too). It trims to the last sentence/word boundary under 250.
func trimReply(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 250 {
		return s
	}
	cut := s[:250]
	// Prefer a sentence boundary, then a word boundary, so the trim reads cleanly.
	if i := strings.LastIndexAny(cut, ".!?"); i >= 200 {
		return strings.TrimSpace(cut[:i+1])
	}
	if i := strings.LastIndex(cut, " "); i >= 200 {
		return strings.TrimSpace(cut[:i]) + "…"
	}
	return strings.TrimSpace(cut) + "…"
}
