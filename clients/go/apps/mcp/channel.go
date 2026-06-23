package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/sx"
)

// channelMethod is the Claude Code channel notification (research preview):
// the harness injects each one into the session as a <channel> event.
const channelMethod = "notifications/claude/channel"

// deliveryCaveat rides every subscribe result: the harness gives a server no
// way to learn whether it was loaded as a channel — without the flag, pushes
// drop silently. The `subscribed` notice that follows a subscribe is the
// agent-side check (see the skill).
const deliveryCaveat = "subscribed. CAVEAT: channel delivery cannot be verified server-side — " +
	"a `subscribed` system notice should arrive as a <channel> event now; if it does not, " +
	"this session was not started with --dangerously-load-development-channels (or org policy " +
	"blocks channels) and you must poll with message_read instead."

// capturingTransport stashes the Connection the server runs over, so the hub
// can write channel notifications onto it. Connection.Write is documented
// safe for concurrent use. (Proven against the live harness, 2026-06-10.)
type capturingTransport struct {
	inner mcp.Transport
	mu    sync.Mutex
	conn  mcp.Connection
}

func (t *capturingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()
	return conn, nil
}

func (t *capturingTransport) notify(ctx context.Context, method string, params any) error {
	t.mu.Lock()
	conn := t.conn
	t.mu.Unlock()
	if conn == nil {
		return errors.New("transport not connected yet")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	// A jsonrpc Request with a zero ID is a notification.
	return conn.Write(ctx, &jsonrpc.Request{Method: method, Params: raw})
}

// channelHub owns the subject → subscription map and renders bus frames into
// channel events. Meta keys are alphanumeric+underscore only — the harness
// silently drops others.
type channelHub struct {
	notify func(ctx context.Context, method string, params any) error
	names  *nameCache
	echo   *selfEchoSet
	selfID atomic.Pointer[string] // this client's ULID, set on connect (self-echo timing grace)

	// state is the durable per-session store (TASK-124): the subjects this hub
	// follows + each one's last delivered seq, mirrored to disk so a fresh MCP
	// process (resume / compaction / restart) can restore them. Never nil — the
	// constructor defaults it to an in-memory-only state; main wires the real one.
	state *substate
	// delivered de-dups the post-resume overlap: restore subscribes live and then
	// backfills the missed frames, so a frame can arrive on both paths. Keyed on
	// the unique frame id.
	delivered *idRing

	// catchingUp gates the LIVE cursor advance for a subject whose restore catch-up
	// is still outstanding (TASK-124). Restore subscribes live before backfilling,
	// so without this a live frame in that window would advance the durable cursor
	// PAST the un-backfilled gap — and a then-failed catch-up would turn that into
	// permanent loss. While set, live frames still deliver but do not advance; the
	// catch-up advances the cursor through the gap and clears the gate.
	cuMu       sync.Mutex
	catchingUp map[string]bool

	// gen is bumped each time a client is discarded (TASK-124). The async restore
	// captures it at spawn and bails if it changes, so a restore running against a
	// now-replaced client can't re-add stale subscriptions into h.subs after
	// discardClient cleared them (which would make the new client's restore skip
	// rebinding them as "already active").
	genMu sync.Mutex
	gen   uint64

	mu   sync.Mutex
	subs map[string]sextant.Subscription

	// inboxDrains tracks one inbox-drain goroutine per live client object, so the
	// drain starts exactly once per connection (idempotent) and can be stopped
	// when the connManager discards that client.
	inboxMu     sync.Mutex
	inboxDrains map[*sextant.Client]chan struct{}
}

func newChannelHub(notify func(ctx context.Context, method string, params any) error, names *nameCache) *channelHub {
	return &channelHub{
		notify: notify,
		names:  names,
		echo:   newSelfEchoSet(),
		// In-memory-only default (path ""): real durability is wired by main,
		// which overrides state with the session-keyed store. Never nil so the
		// subscribe/frameEvent paths can call it unconditionally (and tests get a
		// working in-memory store for free).
		state:       loadSubstate("", ""),
		delivered:   newIDRing(),
		catchingUp:  map[string]bool{},
		subs:        map[string]sextant.Subscription{},
		inboxDrains: map[*sextant.Client]chan struct{}{},
	}
}

// startCatchUp / endCatchUp / isCatchingUp gate a subject's LIVE cursor advance
// while its restore catch-up is in flight (TASK-124, see the catchingUp field).
func (h *channelHub) startCatchUp(subject string) {
	h.cuMu.Lock()
	h.catchingUp[subject] = true
	h.cuMu.Unlock()
}

func (h *channelHub) endCatchUp(subject string) {
	h.cuMu.Lock()
	delete(h.catchingUp, subject)
	h.cuMu.Unlock()
}

func (h *channelHub) isCatchingUp(subject string) bool {
	h.cuMu.Lock()
	defer h.cuMu.Unlock()
	return h.catchingUp[subject]
}

// startInboxDrain bridges the SDK's auto-inbox channel (c.Inbox(), TASK-55) into the
// SAME channel-wake path an explicit message_subscribe uses (frameEvent), so a
// principal's DM to msg.client.<self> WAKES the session (ADR-0030, review M1).
// This is the production consumer of c.Inbox(); without it a DM lands in the
// durable stream but nothing wakes the worker, and the trust hook — which can
// only run on an already-woken turn — never fires until some unrelated turn.
//
// It does NOT open a second bus subscription: TASK-55 already auto-subscribes
// to msg.client.<self> and fans frames into c.Inbox(); a second subscribe would
// double-relay every DM. We only give that existing channel a real reader.
//
// Idempotent + reconnect-safe + leak-free:
//   - One drain per client object: a repeat call for the same client is a no-op,
//     so the connManager calling this on every get() starts it exactly once.
//   - The SDK re-establishes the auto-inbox subscription across reconnect on the
//     same client object (startResumePass → reestablishSubs), so c.Inbox() keeps
//     flowing and this one drain spans the whole cached-client lifecycle.
//   - The goroutine exits on c.Drained() (a cooperative bus drain) or when
//     stopInboxDrain closes its stop channel (the connManager discarding the
//     client). It never blocks the SDK delivery goroutine: frameEvent's push is
//     non-fatal and bounded.
func (h *channelHub) startInboxDrain(c *sextant.Client) {
	// Record this client's id for the self-echo timing grace (frameEvent). Set
	// on every connect (same id across reconnect); cheap and idempotent.
	id := c.ID()
	h.selfID.Store(&id)

	h.inboxMu.Lock()
	if _, running := h.inboxDrains[c]; running {
		h.inboxMu.Unlock()
		return
	}
	stop := make(chan struct{})
	h.inboxDrains[c] = stop
	h.inboxMu.Unlock()

	go func() {
		// Warm the name cache before draining so the first DM resolves its
		// sender to a display name instead of a raw id. Done here, off the
		// connManager's get() critical section (a refresh re-enters get()), and
		// off the hot path (frameEvent stays cached-only on the delivery side).
		h.names.refresh(context.Background())
		h.drainLoop(c.Inbox(), c.Drained(), stop)
	}()
}

// drainLoop forwards each DM into the shared emit path (frameEvent) until the
// connection drains or stop fires. Split out from startInboxDrain so the bridge
// behavior is unit-testable with plain channels, independent of a live client.
func (h *channelHub) drainLoop(inbox <-chan sextant.Message, drained <-chan struct{}, stop <-chan struct{}) {
	for {
		select {
		case m := <-inbox:
			// Same emit logic as an explicit subscription: self-echo drop
			// first, then the wake-vs-content branch.
			h.frameEvent(m)
		case <-drained:
			return
		case <-stop:
			return
		}
	}
}

// stopInboxDrain stops the drain for a client the connManager is discarding (a
// drained connection it is about to replace). Idempotent; safe if no drain ran.
func (h *channelHub) stopInboxDrain(c *sextant.Client) {
	h.inboxMu.Lock()
	stop, ok := h.inboxDrains[c]
	if ok {
		delete(h.inboxDrains, c)
	}
	h.inboxMu.Unlock()
	if ok {
		close(stop)
	}
}

// discardClient is the connManager's onDiscard hook (TASK-124): a cached client
// drained and is about to be replaced. Stop its inbox drain AND drop the manual
// subscriptions bound to it — they died with the connection, and leaving the
// stale entries in h.subs would make restoreSubs (which calls subscribe and
// returns early on an already-present subject) skip rebinding them on the fresh
// client, so the replacement would silently receive no manual-subscription
// events. The subjects stay in the durable substate, so restoreSubs re-opens
// each on the new client and catches it up from its cursor.
func (h *channelHub) discardClient(c *sextant.Client) {
	// Bump the generation first, so an in-flight restore against this (old) client
	// bails before it can re-add a subscription after the clear below.
	h.genMu.Lock()
	h.gen++
	h.genMu.Unlock()
	h.stopInboxDrain(c)
	h.mu.Lock()
	for subject, sub := range h.subs {
		sub.Stop()
		delete(h.subs, subject)
	}
	h.mu.Unlock()
}

func (h *channelHub) generation() uint64 {
	h.genMu.Lock()
	defer h.genMu.Unlock()
	return h.gen
}

// event pushes one channel notification; failures are logged, never fatal —
// the bus subscription must outlive a harness hiccup. It returns the push error
// so frameEvent only advances the durable catch-up cursor after a frame was
// actually delivered (a failed push leaves the cursor for the next restore to
// re-read).
func (h *channelHub) event(content string, meta map[string]any) error {
	if err := h.notify(context.Background(), channelMethod, map[string]any{
		"content": content,
		"meta":    meta,
	}); err != nil {
		log.Printf("channel push failed: %v", err)
		return err
	}
	return nil
}

// wakeOnlyMode reports whether SEXTANT_MCP_WAKE_ONLY=1 is set. In wake-only
// mode frameEvent pushes a content-less notification (subject + wake marker +
// seq) instead of the message body. The auth hook (TASK-56) is then the sole
// delivery path for message content.
func wakeOnlyMode() bool {
	return os.Getenv("SEXTANT_MCP_WAKE_ONLY") == "1"
}

// SPIKE (TASK-57): Wake-only channel mode
// Whether a content-less MCP channel notification wakes an idle Claude Code
// session is unproven — MCP channel notifications may require a non-empty
// content payload to trigger the harness. WAKE_ONLY mode
// (SEXTANT_MCP_WAKE_ONLY=1) is implemented here behind a toggle. Live
// validation is deferred to the TASK-53 demo.
// Fallback: CONTENT mode (default) + TASK-56's hook being authoritative (the
// hook stamps and injects the trusted copy; the agent is instructed to
// disregard the channel-delivered wrapped copy and trust only the
// hook-injected additionalContext).
//
// Channel-validate + Monitor fallback (AC#4):
// The subscribe() method below emits a systemEvent("subscribed", subject, ...)
// immediately after the bus subscription succeeds. This notice arrives at the
// agent as a <channel> event with meta event="subscribed" and subject=<subject>.
// TASK-58's skill must instruct the agent: if the "subscribed" channel event
// does NOT arrive within a turn of calling message_subscribe, fall back to
// running `sextant subscribe <subject>` as a background Monitor process (the
// CLI tails live frames to stdout). The agent drives it via the harness Monitor
// tool or tmux. This is the channel-validate + Monitor fallback path.
// CLI syntax: sextant subscribe <subject>            (live tail, Ctrl-C to stop)
//
//	sextant subscribe <subject> --all         (replay history first, then live)

// isSelfEcho reports whether a delivered frame is this process's own publish
// echoing back on a subscribed subject. The id-based echo set (TASK-52) is the
// source of truth, but there is a race: the bus can relay a self-published
// frame back (via the auto-inbox bridge or an explicit subscription) before
// message_publish has finished recording its id in the set. So when a frame is
// authored by THIS client but its id is not yet in the set, briefly wait for
// the record to land before concluding it is not ours. The wait is bounded and
// the final decision stays id-based: a genuine co-identity frame (one this
// process never published, so its id never lands) is delivered after the grace,
// preserving TASK-52's "a co-identity session still sees frames it didn't
// publish". Frames authored by anyone else take the fast path (no wait).
func (h *channelHub) isSelfEcho(id, author string) bool {
	if h.echo.contains(id) {
		return true
	}
	self := h.selfID.Load()
	if self == nil || *self == "" || author != *self {
		return false
	}
	for i := 0; i < 25 && !h.echo.contains(id); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return h.echo.contains(id)
}

// frameEvent renders a delivered message. chat.message renders as its text;
// any other lexicon as its compact JSON (content is opaque to the bus —
// rendering is a courtesy, not policy).
//
// Frames whose id is in the self-echo set are dropped: they are the result of
// this session's own message_publish being relayed back through the
// subscription (AC#1). Suppression is id-based, not author-based — a resumed
// or co-identity session that holds a different selfEchoSet still sees its
// own frames.
//
// In WAKE_ONLY mode (SEXTANT_MCP_WAKE_ONLY=1) the push carries only a wake
// marker, the subject, and the message sequence — no message body. The auth
// hook (TASK-56) is then the sole content delivery path. In CONTENT mode
// (default, env var absent or not "1") the full frame body is pushed as
// before.
func (h *channelHub) frameEvent(m sextant.Message) {
	// Live delivery: emit, then advance the durable catch-up cursor to the NEXT
	// seq to read — m.Sequence+1, because message_read's `since` is inclusive (the
	// attest cursor stores the next sequence the same way). Only AFTER a confirmed
	// push, biasing re-delivery over loss: a failed push (or a crash before this)
	// leaves the cursor, so the next restore re-reads the frame. Skipped for
	// untracked subjects (the auto-inbox / self-DM are not in substate), and while
	// this subject's restore catch-up is outstanding — advancing past the
	// un-backfilled gap there would risk losing it (catchUp owns the cursor until
	// the gap is closed).
	if err := h.emit(m); err == nil && m.Sequence > 0 && !h.isCatchingUp(m.Subject) {
		h.state.advance(m.Subject, m.Sequence+1)
	}
}

// emit renders one frame and pushes it as a channel event — the shared delivery
// path for live frames (frameEvent) and the restore catch-up (catchUp). It
// returns the push error so a caller advances its cursor ONLY after a confirmed
// delivery. A self-echo or an already-delivered duplicate is intentionally
// dropped and reported as nil (handled — the cursor may advance past it); only a
// real push failure returns non-nil, so the caller leaves the cursor for the
// next restore to re-read.
func (h *channelHub) emit(m sextant.Message) error {
	// Self-echo check is always first, before any wake/content branching.
	if h.isSelfEcho(m.Frame.ID, m.Frame.Author) {
		return nil // our own publish echoing back; suppress it
	}
	// Drop a frame ALREADY delivered this process — the restore catch-up and the
	// live subscription can both carry a frame in the brief overlap after a resume
	// (TASK-124). The id is recorded only AFTER a successful push (below), so a
	// failed push never marks a frame delivered: a later catch-up of the same
	// frame re-pushes it instead of being silently dropped here. The trade-off is
	// a rare double-delivery if two concurrent pushes race between this check and
	// the record — benign (a repeated channel event) versus the loss it prevents.
	if h.delivered.contains(m.Frame.ID) {
		return nil
	}

	push := func(content string, meta map[string]any) error {
		if err := h.event(content, meta); err != nil {
			return err
		}
		h.delivered.record(m.Frame.ID) // mark delivered only on a confirmed push
		return nil
	}

	if wakeOnlyMode() {
		// Wake-only: push a minimal notification — no message body. The auth
		// hook (TASK-56) delivers the trusted, signed content as additionalContext.
		wake, _ := json.Marshal(map[string]any{
			"wake":    true,
			"subject": m.Subject,
			"seq":     m.Sequence,
		})
		return push(string(wake), map[string]any{
			"subject": m.Subject,
			"seq":     fmt.Sprint(m.Sequence),
			"id":      m.Frame.ID,
			"wake":    "1",
		})
	}
	// CONTENT mode (default): push the full frame body.
	content := string(m.Frame.Record)
	var rec struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Frame.Record, &rec); err == nil && rec.Type == "chat.message" {
		content = rec.Text
	}
	return push(content, map[string]any{
		"subject": m.Subject,
		// Cached-only: this runs on the SDK delivery goroutine, where a
		// directory call would stall delivery. A cold cache renders the raw
		// id and the async refresh names the frames after it.
		"sender":    h.names.displayNameCached(m.Frame.Author),
		"sender_id": m.Frame.Author,
		"seq":       fmt.Sprint(m.Sequence),
		"id":        m.Frame.ID,
	})
}

// systemEvent is a notice from this server, not a bus frame: event names the
// condition, and there are no frame attributes to carry. Returns the push error
// so a caller whose correctness depends on the notice arriving (the over-cap
// catch-up hand-off) can react to a failed push.
func (h *channelHub) systemEvent(event, subject, content string) error {
	meta := map[string]any{"event": event}
	if subject != "" {
		meta["subject"] = subject
	}
	return h.event(content, meta)
}

// subscribe follows subject live, delivering via the channel. Duplicate
// subscribes are idempotent. deliver="all" replays retained history first.
//
// restoring distinguishes the two callers (TASK-124). The tool path (an agent's
// message_subscribe, restoring=false) PERSISTS the subject before opening the
// relay and rolls that back if the subscribe fails — a never-established
// subscription must not linger in the durable set. The restore path
// (restoring=true) operates on an ALREADY-persisted subject, so it neither
// re-persists nor rolls back: a transient restore failure must leave the subject
// in the durable set so the NEXT resume retries it (rolling back would turn a
// blip into permanent loss).
func (h *channelHub) subscribe(ctx context.Context, c *sextant.Client, subject, deliver string, restoring bool) ([]string, error) {
	// The client's own DM is already delivered to the channel by the auto-DM
	// bridge (startInboxDrain, TASK-55/M1). Opening a second relay here would push
	// every DM into the session twice. Treat an explicit subscribe to it as
	// already-active: emit the subscribed notice (so the channels-enabled check
	// still confirms) but do NOT open a redundant relay.
	if subject == sx.ClientSubject(c.ID()) {
		_ = h.systemEvent("subscribed", subject, fmt.Sprintf("now following %s (your own DM — delivered automatically on connect); frames arrive as channel events", subject))
		// Report it as active (it IS being delivered, via the bridge) without a
		// second relay. active() reads h.subs, which never holds the self-DM, so
		// this can't duplicate across repeated calls.
		return append(h.active(), subject), nil
	}

	h.mu.Lock()
	_, exists := h.subs[subject]
	h.mu.Unlock()
	if exists {
		return h.active(), nil
	}

	var opts []sextant.SubOption
	if deliver == "all" {
		opts = append(opts, sextant.DeliverAll())
	}
	// Resume failures must be loud (the silent delivers-nothing window is the
	// failure mode this server exists to kill). The handler must not block:
	// hand off to a goroutine.
	opts = append(opts, sextant.OnError(func(err error) {
		go h.resumeNotice(subject, err)
	}))

	// Warm the name cache on the tool path, so the first delivered frames
	// resolve senders without the delivery goroutine ever touching the bus.
	h.names.refresh(ctx)

	// Persist the subscription BEFORE opening the relay (TASK-124): the bus can
	// deliver the first frame before c.Subscribe returns, and frameEvent ignores
	// an untracked subject — so a late addSubject would let that first frame fail
	// to prime the cursor, and a resume before the next frame would then skip
	// catch-up and lose messages. Idempotent (a restored subject is already
	// present; seq 0 = unprimed). The self-DM returned early above, so the durable
	// set holds exactly the manual subjects a restore must re-establish. The
	// restore path skips this — the subject is already persisted with its cursor.
	inserted := false
	if !restoring {
		inserted = h.state.addSubject(subject, deliver)
	}

	sub, err := c.Subscribe(ctx, subject, h.frameEvent, opts...)
	if err != nil {
		// Roll back the persist on a real failure of the TOOL path — but ONLY the
		// entry THIS call inserted (inserted==true), and only if no concurrent
		// subscribe already made it live. If the subject was already persisted (a
		// post-resume re-subscribe before restore rebound it, or a prior entry),
		// addSubject was a no-op and we must not erase it — a transient failure
		// would otherwise lose a known subscription. The restore path never rolls
		// back: a transient failure there leaves the subject for the next resume.
		if inserted {
			h.mu.Lock()
			_, live := h.subs[subject]
			h.mu.Unlock()
			if !live {
				h.state.removeSubject(subject)
			}
		}
		return nil, err
	}
	h.mu.Lock()
	if _, raced := h.subs[subject]; raced {
		// A concurrent subscribe to the same subject won; keep its subscription
		// and stop this one (idempotent semantics, no leak). The subject stays
		// persisted — the winner holds it live.
		h.mu.Unlock()
		sub.Stop()
		return h.active(), nil
	}
	h.subs[subject] = sub
	h.mu.Unlock()

	_ = h.systemEvent("subscribed", subject, fmt.Sprintf("now following %s — frames arrive as channel events; reply with message_publish", subject))
	return h.active(), nil
}

func (h *channelHub) resumeNotice(subject string, err error) {
	if errors.Is(err, sextant.ErrResumeDeferred) {
		_ = h.systemEvent("resume_deferred", subject,
			fmt.Sprintf("subscription to %s is paused by a transport blip; the SDK retries on the next reconnect — nothing delivers until then (%v)", subject, err))
		return
	}
	// Fatal: the bus said the resume is impossible; the subscription is
	// stopped. The tail is gone — the agent must re-read.
	h.mu.Lock()
	delete(h.subs, subject)
	h.mu.Unlock()
	// Reset the durable cursor: the old stream position is meaningless now (a
	// wiped store / expired history restarts at low sequences), so leaving the
	// stale high Seq would wedge a later restore or re-subscribe. The subject
	// stays tracked, so the next restore re-establishes it live from a clean slate
	// (TASK-124). Clear any catch-up gate too — the old gap is moot, and a leftover
	// gate would stop a later re-subscribe's live frames from advancing the cursor.
	h.state.resetCursor(subject)
	h.endCatchUp(subject)
	_ = h.systemEvent("resume_lost", subject,
		fmt.Sprintf("subscription to %s is LOST (%v) — messages may have been missed; catch up with message_read from your last seen cursor, then message_subscribe again", subject, err))
}

// unsubscribe stops following subject; the remaining active list is returned.
func (h *channelHub) unsubscribe(subject string) ([]string, error) {
	h.mu.Lock()
	sub, ok := h.subs[subject]
	if ok {
		delete(h.subs, subject)
	}
	h.mu.Unlock()
	if ok {
		sub.Stop()
	}
	// Clear any leftover catch-up gate. A prior restore catch-up that failed
	// leaves catchingUp[subject] set on purpose (so live frames don't advance past
	// the open gap); once the agent explicitly unsubscribes, the subject is gone,
	// so a later re-subscribe in the same process must not inherit that stale gate
	// — it would silently wedge the new subscription's cursor (TASK-124).
	h.endCatchUp(subject)
	// Drop it from the durable store too, so a restore won't re-establish a
	// subscription the agent explicitly stopped (TASK-124). Crucially this runs
	// even with NO live h.subs entry: right after a resume the subject may be
	// persisted but not yet re-bound by the async restore, and the agent's stop
	// must still be honored. It is "not subscribed" only when it is in neither
	// the live map nor the durable store.
	removed := h.state.removeSubject(subject)
	if !ok && !removed {
		return nil, errNotSubscribed(subject, h.active())
	}
	return h.active(), nil
}

func (h *channelHub) active() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	subjects := make([]string, 0, len(h.subs))
	for s := range h.subs {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	return subjects
}

// catchUpCap bounds how many missed frames a single subject replays on restore,
// so a long offline gap can't flood the session with thousands of channel
// events. Past the cap the agent is told to message_read the rest itself.
const catchUpCap = 1000

// restoreSubs re-establishes the persisted manual subscriptions on a freshly
// connected client and catches each up from its last delivered seq (TASK-124).
// A fresh MCP process (resume / compaction / restart) starts with an empty
// in-memory sub map while the durable substate still names the subjects the
// agent was following — without this, those manual subscriptions silently stop
// delivering (modes A/B). The auto-inbox is handled separately (startInboxDrain,
// and the SDK re-establishes it across reconnect); only the manual subjects are
// in substate. Runs async because catch-up does network I/O and must not block
// the connManager's get() (which holds its mutex while calling onConnect).
func (h *channelHub) restoreSubs(c *sextant.Client) {
	subjects := h.gatePrimedForRestore()
	if len(subjects) == 0 {
		return
	}
	go h.restore(context.Background(), c, subjects, h.generation())
}

// gatePrimedForRestore is restoreSubs' SYNCHRONOUS half: snapshot the persisted
// subjects and gate every PRIMED one's live cursor-advance NOW — before
// restoreSubs spawns the async restore and before any tool message_subscribe can
// run (a tool call shares the connManager mutex via get→onConnect, so it proceeds
// only after restoreSubs returns). Otherwise a re-subscribe of an already-persisted
// primed subject that races the restore could open an UNGATED relay, letting a live
// frame advance the cursor past the un-backfilled gap (TASK-124, the K class).
// restore's catchUp clears each gate as it closes that subject's gap. Returns the
// snapshot so the async restore works the SAME set it gated. Split out so a test
// can exercise this pre-goroutine path without a live client (the goroutine does
// network I/O); restoreSubs is the only production caller.
func (h *channelHub) gatePrimedForRestore() map[string]subjectCursor {
	_, subjects := h.state.snapshot()
	for subject, sc := range subjects {
		if sc.Seq > 0 {
			h.startCatchUp(subject)
		}
	}
	return subjects
}

// restore re-subscribes each subject then backfills the frames missed while it
// was dead. Order matters: subscribe LIVE first, then read (since, now] — the
// union has no gap (live covers [subscribe, ∞), the read covers the prior
// window) and the only overlap is dropped by the delivered ring. The reverse
// order risks a gap (frames between the read snapshot and the subscribe taking
// effect would be delivered by neither).
func (h *channelHub) restore(ctx context.Context, c *sextant.Client, subjects map[string]subjectCursor, gen uint64) {
	for subject, sc := range subjects {
		// Bail if this client was discarded (reconnect / context_use) since the
		// restore began: continuing would re-add subscriptions bound to the now-
		// closed client AFTER discardClient cleared them, and the replacement
		// client's restore would then skip rebinding them as "already active".
		// This closes the common case (a discard between iterations). KNOWN BOUND
		// (TASK-124): a discard landing DURING a single subject's subscribe call can
		// still leave one stale entry — bounded + self-healing (the next reconnect/
		// resume clears h.subs and rebinds) and caught by the liveness PR's seq-gap;
		// fully closing it needs per-generation sub identity, deferred as not worth
		// the machinery for that window (see ADR-0037).
		if h.generation() != gen {
			return
		}
		// The snapshot can be stale: the agent may have called message_unsubscribe
		// after it was taken (removing the subject from the durable store) but
		// before this loop reached the subject. Re-check it's still tracked, so a
		// restore never re-opens a subscription the agent explicitly stopped.
		if !h.state.tracked(subject) {
			continue
		}

		// A wildcard (msg.topic.> / msg.*.x) is handled FIRST and on its own terms:
		// its delivered frames carry concrete subjects, so its stored cursor never
		// advances (it stays Seq 0), and FetchMessages can't label a replayed
		// frame's subject — so a wildcard can be neither cursor-tracked nor caught
		// up per-frame. Restore it LIVE (deliver="new") and ALWAYS notify the agent
		// to fill the downtime gap by reading the concrete subjects it cares about.
		if isWildcardSubject(subject) {
			if _, err := h.subscribe(ctx, c, subject, "new", true); err != nil {
				_ = h.systemEvent("resume_lost", subject, fmt.Sprintf(
					"could not restore the wildcard %s after a resume (%v) — message_subscribe again", subject, err,
				))
				continue
			}
			_ = h.systemEvent("subscribed", subject, fmt.Sprintf(
				"restored the wildcard %s live; a wildcard can't be caught up per-frame (a read can't label each frame's subject) — message_read the concrete subjects you need to fill the downtime gap", subject,
			))
			continue
		}

		// An UNPRIMED concrete subject (Seq 0, no frame delivered before the resume)
		// is restored according to the mode it was subscribed with: a "new"
		// subscription resumes live-only; an "all" subscription re-subscribes
		// deliver="all" so the bus replays the retained history live, NOT dropping
		// the downtime window. A PRIMED subject (Seq > 0) always resumes
		// deliver="new" and catches up from its cursor — re-replaying the whole
		// backlog would flood.
		//
		// KNOWN BOUND (TASK-124): an unprimed deliver="new" subscription on an idle
		// topic that resumes BEFORE its first frame can miss frames published in the
		// dead window — its live relay starts after them, and we can't back-fill
		// without the subscribe-point sequence (reading from 0 would wrongly replay
		// pre-subscribe history). Closing it needs the stream tail at subscribe time,
		// which the bus does not expose MCP-side — a core getter this PR's MCP-only
		// scope defers to the liveness PR (the seq-gap watchdog + heartbeat, already
		// core-touching). A subscription that has delivered ANY frame is primed and
		// resumes losslessly; this bound is the never-yet-delivered idle case only.
		deliver := "new"
		if sc.Seq == 0 && sc.Deliver == "all" {
			deliver = "all"
		}
		// Primed subjects' live-advance gate was already set in restoreSubs
		// (synchronously, before any tool subscribe could race), and catchUp clears
		// it once the gap closes. Unprimed subjects have no gap to protect.
		primed := sc.Seq > 0
		if _, err := h.subscribe(ctx, c, subject, deliver, true); err != nil {
			if primed {
				h.endCatchUp(subject)
			}
			_ = h.systemEvent("resume_lost", subject, fmt.Sprintf(
				"could not restore the subscription to %s after a resume (%v) — catch up with message_read, then message_subscribe again", subject, err,
			))
			continue
		}
		if !primed {
			continue // unprimed: deliver="all" replayed history live above; "new" is live-only
		}
		// Clear the live-advance gate ONLY if catch-up closed the gap. On a
		// transient catch-up failure the gate stays set, so a live frame can't
		// advance the cursor past the still-open gap — the cursor holds at sc.Seq
		// and the next resume retries the catch-up from there (no silent skip).
		if h.catchUp(ctx, c.FetchMessages, subject, sc.Seq) {
			h.endCatchUp(subject)
		}
	}
}

// isWildcardSubject reports whether subject is a NATS wildcard — a `*` token or
// a trailing `>`. The bus can read a wildcard, but the read does not carry each
// frame's concrete subject, so a wildcard is restored live-only (see restore).
func isWildcardSubject(subject string) bool {
	return strings.ContainsAny(subject, "*>")
}

// catchUp replays the frames missed on subject while its subscription was dead
// (stream seq > since), delivering each through the shared emit path so the
// agent sees them as channel events. It pages with the read cursor and bounds
// the total at catchUpCap. Backfilled frames carry Sequence 0 (FetchMessages
// does not expose a per-frame seq), so catchUp advances the durable cursor
// itself — but only PER COMPLETED PAGE, and only once every frame in the page
// was actually pushed. A push failure stops the catch-up and leaves the cursor
// at the page start, so the next restore re-reads from there (re-delivery beats
// silently skipping an undelivered frame — the live path makes the same choice).
//
// Returns true when the gap is closed (caught up, or capped + the agent notified
// to read the remainder) and false when it stopped on a transient read/push
// failure with the gap still open — the caller keeps the cursor gated on false so
// a live frame can't advance past the unclosed gap (and the next resume retries).
func (h *channelHub) catchUp(ctx context.Context, fetch fetchFunc, subject string, since uint64) bool {
	const pageSize = 200
	delivered := 0
	cursor := since
	for {
		// Stop if the agent unsubscribed mid-backfill (the subject left the durable
		// store): a stale catch-up must not keep replaying frames for a subscription
		// the agent explicitly stopped (TASK-124). Treated as "done" — the gap is
		// moot once unsubscribed.
		if !h.state.tracked(subject) {
			return true
		}
		frames, next, err := fetch(ctx, subject, cursor, pageSize)
		if err != nil {
			_ = h.systemEvent("resume_deferred", subject, fmt.Sprintf(
				"catch-up read on %s failed (%v); live delivery continues — message_read from cursor %d to fill the gap", subject, err, cursor,
			))
			return false
		}
		for i := range frames {
			if delivered >= catchUpCap {
				// The cap hands the remainder off to the agent via this notice — so
				// correctness depends on it arriving. If the push fails, treat it like
				// a deferred catch-up: return false (keep the gate + cursor) so a live
				// frame can't advance past the undelivered remainder, and the next
				// resume retries (re-reading the page + re-issuing the notice).
				if err := h.systemEvent("subscribed", subject, fmt.Sprintf(
					"restored %s; more than %d missed frames — message_read from cursor %d for the remainder", subject, catchUpCap, cursor,
				)); err != nil {
					return false
				}
				h.state.advance(subject, cursor)
				return true // bounded + agent notified: the agent owns the remainder
			}
			// Shape guard: skip a malformed retained entry (a raw NATS publisher or
			// store corruption could place one on msg.*), matching the live path's
			// quarantine of malformed frames. The live freshness checks (epoch/skew)
			// do NOT apply to replay — a retained frame legitimately carries a prior
			// epoch and an old bus time — and trust is enforced by author regardless
			// of path (the attest hook), so this is the applicable check for replay.
			if frames[i].ID == "" || frames[i].Author == "" {
				log.Printf("sextant-mcp: catch-up skipped a malformed frame on %s", subject)
				continue
			}
			if emitErr := h.emit(sextant.Message{Frame: frames[i], Subject: subject}); emitErr != nil {
				// A push failed mid-page: stop and leave the cursor at this page's
				// start so the next restore re-reads from here rather than skipping
				// the undelivered frame.
				_ = h.systemEvent("resume_deferred", subject, fmt.Sprintf(
					"catch-up push on %s failed (%v); message_read from cursor %d to fill the gap", subject, emitErr, cursor,
				))
				return false
			}
			delivered++
		}
		if next <= cursor {
			break // no progress past the cursor: caught up
		}
		// Advance even when the page decoded EMPTY but next moved — a page of
		// undecodable/raw entries (the bus skips them but still advances next) must
		// not wedge the cursor, or it would be re-read every restore and the valid
		// missed messages behind it never reached.
		cursor = next
		h.state.advance(subject, cursor)
	}
	h.state.advance(subject, cursor)
	return true
}

type subscribeArgs struct {
	Subject  string `json:"subject" jsonschema:"exact subject or wildcard to follow (e.g. msg.topic.plan)"`
	Deliver  string `json:"deliver,omitempty" jsonschema:"new (default) = only messages from now on; all = replay retained history first"`
	SinceSeq uint64 `json:"since_seq,omitempty" jsonschema:"unused yet; catch up explicitly with message_read instead"`
}

func registerMessageSubscribe(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "message_subscribe",
		Description: "Follow a subject live: inbound frames are pushed into this session as <channel> events. Idempotent per subject. Reply path is message_publish.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args subscribeArgs) (*mcp.CallToolResult, any, error) {
		c, err := d.conn.get(ctx)
		if err != nil {
			return toolError(err), nil, nil
		}
		active, err := d.hub.subscribe(context.Background(), c, args.Subject, args.Deliver, false)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"active_subscriptions": active, "note": deliveryCaveat})
		return res, nil, err
	})
}

type unsubscribeArgs struct {
	Subject string `json:"subject" jsonschema:"the subject to stop following"`
}

func registerMessageUnsubscribe(s *mcp.Server, d *deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "message_unsubscribe",
		Description: "Stop following a subject (the channel analogue of Ctrl-C on `sextant subscribe`).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args unsubscribeArgs) (*mcp.CallToolResult, any, error) {
		active, err := d.hub.unsubscribe(args.Subject)
		if err != nil {
			return toolError(err), nil, nil
		}
		res, err := jsonResult(map[string]any{"active_subscriptions": active})
		return res, nil, err
	})
}
