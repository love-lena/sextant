package widget

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/nxadm/tail"

	"github.com/love-lena/sextant/pkg/client"
)

// Event carries one item OR an error from a Source. Terminal marks the
// final event (OnceSource sets it; streams just close the channel). The
// channel never silently drops failures — that's the whole point of
// unifying the drain loop without flattening source semantics.
type Event[T any] struct {
	Item     T
	Err      error
	Terminal bool
}

// Source yields Events until closed: a NATS subscription, a tailed file,
// or a one-shot RPC. The adapter standardizes the plumbing (open → drain
// → close → surface errors); it deliberately does NOT flatten the
// sources' semantics — NATS ack/seq stay on the client.Message item, a
// file tail's errors arrive as Event.Err, an RPC is one terminal Event.
type Source[T any] interface {
	Events() <-chan Event[T]
	Close() error
}

// --- SubscribeSource (NATS) ---

// SubscribeBus is the subscribe-only subset a *client.Client satisfies.
type SubscribeBus interface {
	Subscribe(ctx context.Context, subject string, opts ...client.SubscribeOption) (<-chan client.Message, error)
}

type subscribeSource struct {
	out    chan Event[client.Message]
	cancel context.CancelFunc
}

// SubscribeSource subscribes to subject and forwards each message as an
// Event. Per-message decode errors stay on client.Message.Err (the
// item); a failed initial Subscribe is emitted as a terminal Event.Err.
func SubscribeSource(ctx context.Context, bus SubscribeBus, subject string, opts ...client.SubscribeOption) Source[client.Message] {
	ctx, cancel := context.WithCancel(ctx)
	s := &subscribeSource{out: make(chan Event[client.Message]), cancel: cancel}
	go func() {
		defer close(s.out)
		ch, err := bus.Subscribe(ctx, subject, opts...)
		if err != nil {
			select {
			case s.out <- Event[client.Message]{Err: err, Terminal: true}:
			case <-ctx.Done():
			}
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case s.out <- Event[client.Message]{Item: msg}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return s
}

func (s *subscribeSource) Events() <-chan Event[client.Message] { return s.out }
func (s *subscribeSource) Close() error                         { s.cancel(); return nil }

// --- TailSource (file) ---

type tailSource struct {
	out      chan Event[string]
	t        *tail.Tail
	done     chan struct{}
	closeOne sync.Once
}

// TailSource follows path (tail -f semantics, with rotation). Existing
// content is replayed, then new lines as they land. Read errors arrive
// as Event.Err.
func TailSource(path string) Source[string] {
	s := &tailSource{out: make(chan Event[string]), done: make(chan struct{})}
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true,
		Logger:    tail.DiscardingLogger,
	})
	s.t = t
	go func() {
		defer close(s.out)
		if err != nil {
			select {
			case s.out <- Event[string]{Err: err, Terminal: true}:
			case <-s.done:
			}
			return
		}
		for {
			select {
			case <-s.done:
				return
			case line, ok := <-t.Lines:
				if !ok {
					return
				}
				ev := Event[string]{Item: line.Text}
				if line.Err != nil {
					ev = Event[string]{Err: line.Err}
				}
				select {
				case s.out <- ev:
				case <-s.done:
					return
				}
			}
		}
	}()
	return s
}

func (s *tailSource) Events() <-chan Event[string] { return s.out }

func (s *tailSource) Close() error {
	s.closeOne.Do(func() { close(s.done) })
	if s.t != nil {
		return s.t.Stop()
	}
	return nil
}

// --- OnceSource (one-shot RPC / function) ---

type onceSource[T any] struct {
	out chan Event[T]
}

// OnceSource runs fn once (async) and emits a single terminal Event with
// its result or error.
func OnceSource[T any](fn func() (T, error)) Source[T] {
	s := &onceSource[T]{out: make(chan Event[T], 1)}
	go func() {
		defer close(s.out)
		v, err := fn()
		s.out <- Event[T]{Item: v, Err: err, Terminal: true}
	}()
	return s
}

func (s *onceSource[T]) Events() <-chan Event[T] { return s.out }
func (s *onceSource[T]) Close() error            { return nil }

// --- Pump ---

// Pump drains src into send until the channel closes, a terminal event
// arrives, or ctx cancels. Items route through onItem, errors through
// onErr (either may be nil to ignore). src.Close() runs on return.
func Pump[T any](ctx context.Context, src Source[T], send func(tea.Msg), onItem func(T) tea.Msg, onErr func(error) tea.Msg) {
	defer func() { _ = src.Close() }()
	ch := src.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch {
			case ev.Err != nil:
				if onErr != nil {
					if m := onErr(ev.Err); m != nil {
						send(m)
					}
				}
			default:
				if onItem != nil {
					if m := onItem(ev.Item); m != nil {
						send(m)
					}
				}
			}
			if ev.Terminal {
				return
			}
		}
	}
}
