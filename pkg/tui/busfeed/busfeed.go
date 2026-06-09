// Package busfeed bridges a Sextant SDK subscription into the Bubble Tea event
// loop. It is the seam between the public SDK and the dash's pane-surfaces
// (ADR-0023): a surface embeds a Feed to receive live bus events as tea.Msgs
// without ever touching NATS.
//
// The bridge is the canonical Bubble Tea external-stream pattern. Subscribe
// opens an SDK subscription whose Handler does a non-blocking send onto a
// buffered channel; the re-issued Next command reads one item off that channel,
// off the main loop, and returns it as a typed tea.Msg. On receiving an
// EventMsg the model issues Next again — that re-issue is the pump.
//
// Round-trip merge (ADR-0023): a self-Publish returns on the same subscription,
// so the feed adds no optimistic echo — visible means durably on the bus. The
// feed touches only the public SDK (pkg/sextant) and the public wire atom
// (pkg/wire); no NATS or internal types leak into the TUI.
package busfeed

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/pkg/sextant"
)

// DefaultBuffer is the capacity of the channel between the SDK handler and the
// pump. It absorbs bursts the renderer has not yet drained; on overflow the feed
// drops the incoming event and counts it (see DroppedMsg) rather than blocking
// the handler.
const DefaultBuffer = 256

// SubscribedMsg reports that the SDK subscription is open and live. The model
// issues Next on receiving it to start the pump. Decoupling the open from the
// first (blocking) read keeps "subscribed" observable, so a live-only
// subscription does not miss a message published in the gap between issuing
// Subscribe and the first read.
type SubscribedMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
}

// EventMsg carries one bus event the pump read off the subscription. It wraps
// only the public SDK Message; no NATS or internal type is exposed.
type EventMsg struct {
	// From identifies the emitting feed. A model holding several live feeds (a
	// discovery wildcard plus an opened conversation, say) demultiplexes on it:
	// claim a message when From is your feed, route or ignore it otherwise. The
	// tag matters because a Bubble Tea program delivers every message to every
	// model in its path — without it, one feed's events bleed into another's
	// surface. A nil From is an untagged, test-synthesized message; a real feed
	// always tags.
	From    *Feed
	Message sextant.Message
}

// DroppedMsg reports that N events were dropped because the buffer was full when
// they arrived. It is coalesced: the feed accumulates the count and surfaces it
// as a single message when the pump next runs, so the UI can show one gap marker
// instead of one message per drop. Overflow is fail-loud, never silent.
type DroppedMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
	N    int
}

// ErrMsg reports a subscribe error. The SDK already reconnects the underlying
// bus connection, so the feed adds no reconnect layer of its own; a surfaced
// error is for the UI to show, not for the feed to retry.
type ErrMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
	Err  error
}

// From returns the feed a busfeed message was emitted by, or nil when msg is not
// a busfeed message (or is an untagged, test-synthesized one). Consumers holding
// several feeds use it to demultiplex.
func From(msg tea.Msg) *Feed {
	switch m := msg.(type) {
	case SubscribedMsg:
		return m.From
	case EventMsg:
		return m.From
	case DroppedMsg:
		return m.From
	case ErrMsg:
		return m.From
	}
	return nil
}

// Feed wraps a public SDK subscription as a Bubble Tea stream source. Construct
// it with New, start it from a model's Init with Subscribe, pump it by returning
// Next on every EventMsg and DroppedMsg, and tear it down with Stop. A Feed is
// single-consumer: exactly one Next command is in flight at a time, following the
// pump loop. Use one Feed per surface; a model embedding several feeds
// demultiplexes their messages on the From tag each one carries (see
// EventMsg.From and the package From helper).
type Feed struct {
	client  *sextant.Client
	subject string
	opts    []sextant.SubOption

	events chan sextant.Message

	mu      sync.Mutex
	dropped int                  // events lost to a full buffer, not yet reported
	sub     sextant.Subscription // set once Subscribe succeeds; nil before/after
	stopped bool
}

// New builds a Feed that will subscribe client to subject. The SubOptions are
// passed straight through to the SDK; pass sextant.DeliverAll() to replay the
// backlog before live events. New does not open the subscription — call
// Subscribe (a tea.Cmd) to do that, so the lifecycle stays inside the Bubble Tea
// loop.
func New(client *sextant.Client, subject string, opts ...sextant.SubOption) *Feed {
	return &Feed{
		client:  client,
		subject: subject,
		opts:    opts,
		events:  make(chan sextant.Message, DefaultBuffer),
	}
}

// Subscribe is the tea.Cmd that opens the SDK subscription. The SDK Handler does
// a non-blocking send onto the internal buffer; on a full buffer it drops the
// event and bumps the dropped counter (surfaced later as a DroppedMsg). Subscribe
// returns a SubscribedMsg on success (the model then issues Next to start the
// pump) or an ErrMsg on failure. Issue it once, from the model's Init.
//
// Opening the subscription is decoupled from the first read so that "subscribed"
// is observable: a live-only feed cannot miss a message published between
// issuing Subscribe and the first Next.
//
// ctx scopes the subscription: cancelling it tears the subscription down, the
// same as Stop. Pass a context that lives as long as the surface.
func (f *Feed) Subscribe(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		sub, err := f.client.Subscribe(ctx, f.subject, f.handle, f.opts...)
		if err != nil {
			return ErrMsg{From: f, Err: err}
		}
		f.mu.Lock()
		// If Stop already ran (or ctx is gone), don't hold a live subscription.
		if f.stopped {
			f.mu.Unlock()
			sub.Stop()
			return nil
		}
		f.sub = sub
		f.mu.Unlock()
		return SubscribedMsg{From: f}
	}
}

// handle is the SDK Handler. It runs on an SDK delivery goroutine and must never
// block, so it sends non-blocking: if the buffer is full it drops the event and
// counts it. This is the locked overflow policy — drop, count, surface, never
// block the handler and never ring-buffer.
//
// The send is gated by the stopped flag under the mutex. The SDK's Stop is
// asynchronous (it cancels and tears down on a bridge goroutine), so a delivery
// can race a Stop; gating under the lock that also guards close keeps the send
// from ever reaching a closed channel.
func (f *Feed) handle(m sextant.Message) {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	select {
	case f.events <- m:
	default:
		f.dropped++
	}
	f.mu.Unlock()
}

// Next is the pump step: it reads one event off the buffer, off the main loop,
// and returns it as a tea.Msg. A model returns Next on every EventMsg and every
// DroppedMsg to keep the pump running (ErrMsg is terminal — the feed surfaces it
// and stops reading). If events were dropped since the last step, Next reports
// the coalesced DroppedMsg first (the dropped count is then cleared); the next
// Next resumes reading events. Next returns nil when the feed is stopped and the
// buffer is drained, ending the pump cleanly.
func (f *Feed) Next() tea.Cmd {
	return f.next
}

// next implements one pump step. It is the tea.Cmd body Next returns.
func (f *Feed) next() tea.Msg {
	// Report coalesced drops before reading more, so a gap marker precedes the
	// events that arrived after the gap.
	f.mu.Lock()
	if n := f.dropped; n > 0 {
		f.dropped = 0
		f.mu.Unlock()
		return DroppedMsg{From: f, N: n}
	}
	f.mu.Unlock()

	m, ok := <-f.events
	if !ok {
		// Channel closed by Stop and fully drained: the pump ends here.
		return nil
	}
	return EventMsg{From: f, Message: m}
}

// Stop tears the feed down: it stops the SDK subscription and closes the buffer
// so a blocked Next unblocks and returns nil, ending the pump. It is safe to call
// more than once and safe to call before Subscribe completes. After Stop no
// goroutine or subscription survives (goleak-clean).
func (f *Feed) Stop() {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	f.stopped = true
	sub := f.sub
	f.sub = nil
	f.mu.Unlock()

	if sub != nil {
		sub.Stop()
	}
	close(f.events)
}
