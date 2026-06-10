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
	"errors"
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
// as a single message, so the UI can show one gap marker instead of one message
// per drop. The marker is delivered in stream position — after every event that
// arrived before the gap and before the first event that arrived after it — so
// the gap renders exactly where the loss happened. Overflow is fail-loud, never
// silent.
type DroppedMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
	N    int
}

// ErrMsg reports a FATAL subscription error: a failed subscribe, or a resume
// the bus answered is impossible (e.g. the store was wiped) — the SDK has
// stopped the subscription. It is terminal: the model shows it and must not
// issue Next again. The SDK already reconnects the underlying bus connection,
// so the feed adds no reconnect layer of its own; a surfaced error is for the
// UI to show, not for the feed to retry. A recoverable resume failure is NOT
// an ErrMsg — see ResumeDeferredMsg.
type ErrMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
	Err  error
}

// ResumeDeferredMsg reports the SDK's non-fatal resume notice
// (sextant.ErrResumeDeferred): a reconnect-time resume failed on transport, the
// subscription stays registered, and the next reconnect retries it — until then
// it delivers nothing. It is NOT terminal: the model returns Next on it, the
// same as a DroppedMsg, and shows a transient "reconnecting" notice that it
// clears when events flow again (the deferred resume succeeded). It is
// coalesced: repeated deferrals while one notice is still unread surface as a
// single message. It surfaces after everything already buffered — the events
// it stands between were all delivered before the stall — and never disturbs
// the gap-marker accounting (drops ride the events channel; the notice does
// not).
type ResumeDeferredMsg struct {
	// From identifies the emitting feed (see EventMsg.From).
	From *Feed
	// Err is the SDK's wrapped notice (errors.Is(Err, sextant.ErrResumeDeferred)
	// holds); it names the subject and the underlying transport failure.
	Err error
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
	case ResumeDeferredMsg:
		return m.From
	case ErrMsg:
		return m.From
	}
	return nil
}

// Feed wraps a public SDK subscription as a Bubble Tea stream source. Construct
// it with New, start it from a model's Init with Subscribe, pump it by returning
// Next on every EventMsg, DroppedMsg, and ResumeDeferredMsg (only ErrMsg is
// terminal), and tear it down with Stop. A Feed is
// single-consumer: exactly one Next command is in flight at a time, following the
// pump loop. Use one Feed per surface; a model embedding several feeds
// demultiplexes their messages on the From tag each one carries (see
// EventMsg.From and the package From helper).
type Feed struct {
	client  *sextant.Client
	subject string
	opts    []sextant.SubOption

	events chan item
	// errs receives FATAL mid-stream errors delivered by the SDK's OnError
	// handler (a resume the bus answered is impossible — the subscription is
	// stopped). Capacity 1: there is at most one fatal error per subscription
	// lifetime.
	errs chan error
	// notices receives the SDK's non-fatal ErrResumeDeferred notices (a
	// transport-failed resume the next reconnect retries). Capacity 1 with a
	// non-blocking send coalesces repeated deferrals into one unread notice.
	notices chan error

	mu      sync.Mutex
	dropped int // events lost to a full buffer, not yet carried by an item
	// pending holds the event whose in-band gap (item.gapBefore) the previous
	// pump step just reported as a DroppedMsg; the next step returns it.
	pending *sextant.Message
	sub     sextant.Subscription // set once Subscribe succeeds; nil before/after
	stopped bool
}

// item is one buffered entry on the events channel. gapBefore carries the gap
// in-band: it is the number of events dropped immediately before this one was
// enqueued (zero in the common case), so the pump can place the gap marker
// exactly between the events that arrived before the drops and the first that
// arrived after them — channel order IS stream order.
type item struct {
	msg       sextant.Message
	gapBefore int
}

// New builds a Feed that will subscribe client to subject. The SubOptions are
// passed straight through to the SDK; pass sextant.DeliverAll() to replay the
// backlog before live events. New does not open the subscription — call
// Subscribe (a tea.Cmd) to do that, so the lifecycle stays inside the Bubble Tea
// loop.
//
// The Feed always registers an OnError handler with the SDK and splits its
// two-tier resume-failure contract (ADR-0027) into two pump messages, so
// neither tier goes silent:
//
//   - A non-fatal deferral (wraps sextant.ErrResumeDeferred: the resume failed
//     on transport and the next reconnect retries it) surfaces as a
//     ResumeDeferredMsg. The pump keeps running — the still-registered
//     subscription delivers again once a retry succeeds.
//   - Anything else is fatal (the subscription is stopped) and surfaces as an
//     ErrMsg, which is terminal: Next returns it once, then nil on subsequent
//     calls.
func New(client *sextant.Client, subject string, opts ...sextant.SubOption) *Feed {
	f := &Feed{
		client:  client,
		subject: subject,
		events:  make(chan item, DefaultBuffer),
		errs:    make(chan error, 1),
		notices: make(chan error, 1),
	}
	// Prepend the OnError handler so caller-supplied opts are applied after it;
	// a caller can override with their own OnError if needed (last write wins in
	// subConfig because SubOption is a func that overwrites the field).
	f.opts = append([]sextant.SubOption{sextant.OnError(f.onError)}, opts...)
	return f
}

// onError is the SDK OnError handler. It runs on the NATS client's
// asynchronous-callback goroutine and must not block, so it hands the error to
// a buffered channel and returns: the non-fatal ErrResumeDeferred tier
// (distinguished with errors.Is, per the sextant.OnError contract) to notices,
// everything else — fatal, the subscription is stopped — to errs. Both sends
// are non-blocking onto capacity-1 channels: a duplicate while one is unread
// coalesces (drops), which loses no information — the notice is a state, not a
// count, and there is at most one fatal error per subscription lifetime.
func (f *Feed) onError(err error) {
	ch := f.errs
	if errors.Is(err, sextant.ErrResumeDeferred) {
		ch = f.notices
	}
	select {
	case ch <- err:
	default: // one already queued; coalesce
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
// The first successful enqueue after a drop carries the accumulated count
// in-band (item.gapBefore), so the gap travels through the buffer in stream
// position rather than jumping the queue when the pump next runs.
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
	case f.events <- item{msg: m, gapBefore: f.dropped}:
		f.dropped = 0
	default:
		f.dropped++
	}
	f.mu.Unlock()
}

// Next is the pump step: it reads one event off the buffer, off the main loop,
// and returns it as a tea.Msg. A model returns Next on every EventMsg, every
// DroppedMsg, and every ResumeDeferredMsg to keep the pump running (only ErrMsg
// is terminal — the feed surfaces it and stops reading; a deferred resume is
// recoverable, so its notice must never end the pump — events flow again once
// a later reconnect resumes the subscription). A coalesced DroppedMsg surfaces
// in stream position: after
// every buffered event that arrived before the gap, and before the first event
// that arrived after it (that event is held one step and returned by the next
// Next). Next returns nil when the feed is stopped and the buffer is drained,
// ending the pump cleanly.
func (f *Feed) Next() tea.Cmd {
	return f.next
}

// next implements one pump step. It is the tea.Cmd body Next returns.
func (f *Feed) next() tea.Msg {
	// The previous step reported a gap carried by an event (item.gapBefore) and
	// held the event itself back; deliver it now, right after its marker.
	f.mu.Lock()
	if m := f.pending; m != nil {
		f.pending = nil
		f.mu.Unlock()
		return EventMsg{From: f, Message: *m}
	}
	f.mu.Unlock()

	// Drain already-buffered events before honoring a terminal error, so an
	// ErrMsg cannot preempt up to DefaultBuffer events that were delivered
	// before the failure — the subscriber sees everything it received, then the
	// error. Gap placement rides the channel order: every buffered event arrived
	// BEFORE any event currently being dropped, so events drain first and a gap
	// surfaces only when its carrying item (or the empty buffer, below) says so.
	select {
	case it, ok := <-f.events:
		return f.emit(it, ok)
	default:
	}

	// Buffer empty: a trailing gap — drops not yet carried in-band because no
	// event has been enqueued after them — is reported now, so a burst that ends
	// in losses still surfaces its marker without waiting for the next event.
	f.mu.Lock()
	if n := f.dropped; n > 0 {
		f.dropped = 0
		f.mu.Unlock()
		return DroppedMsg{From: f, N: n}
	}
	f.mu.Unlock()

	// A queued deferred-resume notice surfaces once everything already received
	// has drained (the same everything-then-the-news ordering the terminal error
	// gets): the buffered events all arrived before the stall, and the notice's
	// separate channel never disturbs the gap accounting riding the events. It
	// is not terminal — the model returns Next and the pump keeps running.
	select {
	case err := <-f.notices:
		return ResumeDeferredMsg{From: f, Err: err}
	default:
	}

	// Block on all three channels so a mid-stream error (reconnect failure)
	// surfaces rather than silently starving the pump: a fatal one as a terminal
	// ErrMsg (the caller must not issue Next after receiving one), a deferral as
	// a non-terminal ResumeDeferredMsg.
	select {
	case err := <-f.errs:
		return ErrMsg{From: f, Err: err}
	case err := <-f.notices:
		return ResumeDeferredMsg{From: f, Err: err}
	case it, ok := <-f.events:
		return f.emit(it, ok)
	}
}

// emit turns one channel read into the pump's tea.Msg. An item carrying a gap
// yields the DroppedMsg first and parks the event in pending for the next step,
// so the marker lands exactly between the pre-gap and post-gap events. A closed
// channel (ok=false: Stop ran and the buffer is drained) reports any trailing
// drops, then ends the pump with nil.
func (f *Feed) emit(it item, ok bool) tea.Msg {
	if !ok {
		f.mu.Lock()
		if n := f.dropped; n > 0 {
			f.dropped = 0
			f.mu.Unlock()
			return DroppedMsg{From: f, N: n}
		}
		f.mu.Unlock()
		return nil
	}
	if it.gapBefore > 0 {
		f.mu.Lock()
		f.pending = &it.msg
		f.mu.Unlock()
		return DroppedMsg{From: f, N: it.gapBefore}
	}
	return EventMsg{From: f, Message: it.msg}
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
