package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
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

	mu   sync.Mutex
	subs map[string]sextant.Subscription

	// dmDrains tracks one DM-drain goroutine per live client object, so the
	// drain starts exactly once per connection (idempotent) and can be stopped
	// when the connManager discards that client.
	dmMu     sync.Mutex
	dmDrains map[*sextant.Client]chan struct{}
}

func newChannelHub(notify func(ctx context.Context, method string, params any) error, names *nameCache) *channelHub {
	return &channelHub{
		notify:   notify,
		names:    names,
		echo:     newSelfEchoSet(),
		subs:     map[string]sextant.Subscription{},
		dmDrains: map[*sextant.Client]chan struct{}{},
	}
}

// startDMDrain bridges the SDK's auto-DM channel (c.DMs(), TASK-55) into the
// SAME channel-wake path an explicit message_subscribe uses (frameEvent), so a
// principal's DM to msg.client.<self> WAKES the session (ADR-0030, review M1).
// This is the production consumer of c.DMs(); without it a DM lands in the
// durable stream but nothing wakes the worker, and the trust hook — which can
// only run on an already-woken turn — never fires until some unrelated turn.
//
// It does NOT open a second bus subscription: TASK-55 already auto-subscribes
// to msg.client.<self> and fans frames into c.DMs(); a second subscribe would
// double-relay every DM. We only give that existing channel a real reader.
//
// Idempotent + reconnect-safe + leak-free:
//   - One drain per client object: a repeat call for the same client is a no-op,
//     so the connManager calling this on every get() starts it exactly once.
//   - The SDK re-establishes the auto-DM subscription across reconnect on the
//     same client object (startResumePass → reestablishSubs), so c.DMs() keeps
//     flowing and this one drain spans the whole cached-client lifecycle.
//   - The goroutine exits on c.Drained() (a cooperative bus drain) or when
//     stopDMDrain closes its stop channel (the connManager discarding the
//     client). It never blocks the SDK delivery goroutine: frameEvent's push is
//     non-fatal and bounded.
func (h *channelHub) startDMDrain(c *sextant.Client) {
	// Record this client's id for the self-echo timing grace (frameEvent). Set
	// on every connect (same id across reconnect); cheap and idempotent.
	id := c.ID()
	h.selfID.Store(&id)

	h.dmMu.Lock()
	if _, running := h.dmDrains[c]; running {
		h.dmMu.Unlock()
		return
	}
	stop := make(chan struct{})
	h.dmDrains[c] = stop
	h.dmMu.Unlock()

	go func() {
		// Warm the name cache before draining so the first DM resolves its
		// sender to a display name instead of a raw id. Done here, off the
		// connManager's get() critical section (a refresh re-enters get()), and
		// off the hot path (frameEvent stays cached-only on the delivery side).
		h.names.refresh(context.Background())
		h.drainLoop(c.DMs(), c.Drained(), stop)
	}()
}

// drainLoop forwards each DM into the shared emit path (frameEvent) until the
// connection drains or stop fires. Split out from startDMDrain so the bridge
// behavior is unit-testable with plain channels, independent of a live client.
func (h *channelHub) drainLoop(dms <-chan sextant.Message, drained <-chan struct{}, stop <-chan struct{}) {
	for {
		select {
		case m := <-dms:
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

// stopDMDrain stops the drain for a client the connManager is discarding (a
// drained connection it is about to replace). Idempotent; safe if no drain ran.
func (h *channelHub) stopDMDrain(c *sextant.Client) {
	h.dmMu.Lock()
	stop, ok := h.dmDrains[c]
	if ok {
		delete(h.dmDrains, c)
	}
	h.dmMu.Unlock()
	if ok {
		close(stop)
	}
}

// event pushes one channel notification; failures are logged, never fatal —
// the bus subscription must outlive a harness hiccup.
func (h *channelHub) event(content string, meta map[string]any) {
	if err := h.notify(context.Background(), channelMethod, map[string]any{
		"content": content,
		"meta":    meta,
	}); err != nil {
		log.Printf("channel push failed: %v", err)
	}
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
// frame back (via the auto-DM bridge or an explicit subscription) before
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
	// Self-echo check is always first, before any wake/content branching.
	if h.isSelfEcho(m.Frame.ID, m.Frame.Author) {
		return // our own publish echoing back; suppress it
	}

	if wakeOnlyMode() {
		// Wake-only: push a minimal notification — no message body. The auth
		// hook (TASK-56) delivers the trusted, signed content as additionalContext.
		wake, _ := json.Marshal(map[string]any{
			"wake":    true,
			"subject": m.Subject,
			"seq":     m.Sequence,
		})
		h.event(string(wake), map[string]any{
			"subject": m.Subject,
			"seq":     fmt.Sprint(m.Sequence),
			"id":      m.Frame.ID,
			"wake":    "1",
		})
		return
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
	h.event(content, map[string]any{
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
// condition, and there are no frame attributes to carry.
func (h *channelHub) systemEvent(event, subject, content string) {
	meta := map[string]any{"event": event}
	if subject != "" {
		meta["subject"] = subject
	}
	h.event(content, meta)
}

// subscribe follows subject live, delivering via the channel. Duplicate
// subscribes are idempotent. deliver="all" replays retained history first.
func (h *channelHub) subscribe(ctx context.Context, c *sextant.Client, subject, deliver string) ([]string, error) {
	// The client's own DM is already delivered to the channel by the auto-DM
	// bridge (startDMDrain, TASK-55/M1). Opening a second relay here would push
	// every DM into the session twice. Treat an explicit subscribe to it as
	// already-active: emit the subscribed notice (so the channels-enabled check
	// still confirms) but do NOT open a redundant relay.
	if subject == sx.ClientSubject(c.ID()) {
		h.systemEvent("subscribed", subject, fmt.Sprintf("now following %s (your own DM — delivered automatically on connect); frames arrive as channel events", subject))
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

	sub, err := c.Subscribe(ctx, subject, h.frameEvent, opts...)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if _, raced := h.subs[subject]; raced {
		// A concurrent subscribe to the same subject won; keep its
		// subscription and stop this one (idempotent semantics, no leak).
		h.mu.Unlock()
		sub.Stop()
		return h.active(), nil
	}
	h.subs[subject] = sub
	h.mu.Unlock()

	h.systemEvent("subscribed", subject, fmt.Sprintf("now following %s — frames arrive as channel events; reply with message_publish", subject))
	return h.active(), nil
}

func (h *channelHub) resumeNotice(subject string, err error) {
	if errors.Is(err, sextant.ErrResumeDeferred) {
		h.systemEvent("resume_deferred", subject,
			fmt.Sprintf("subscription to %s is paused by a transport blip; the SDK retries on the next reconnect — nothing delivers until then (%v)", subject, err))
		return
	}
	// Fatal: the bus said the resume is impossible; the subscription is
	// stopped. The tail is gone — the agent must re-read.
	h.mu.Lock()
	delete(h.subs, subject)
	h.mu.Unlock()
	h.systemEvent("resume_lost", subject,
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
	if !ok {
		return nil, errNotSubscribed(subject, h.active())
	}
	sub.Stop()
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
		active, err := d.hub.subscribe(context.Background(), c, args.Subject, args.Deliver)
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
