package widget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/client"
)

func TestOnceSourceEmitsTerminal(t *testing.T) {
	src := OnceSource(func() (int, error) { return 42, nil })
	ev, ok := <-src.Events()
	if !ok {
		t.Fatal("expected one event")
	}
	if ev.Item != 42 || !ev.Terminal || ev.Err != nil {
		t.Fatalf("event = %+v, want {42, terminal, no err}", ev)
	}
	if _, ok := <-src.Events(); ok {
		t.Fatal("channel should be closed after the terminal event")
	}
}

// fakeSource feeds a fixed event slice for Pump tests.
type fakeSource[T any] struct{ ch chan Event[T] }

func (f *fakeSource[T]) Events() <-chan Event[T] { return f.ch }
func (f *fakeSource[T]) Close() error            { return nil }

func TestPumpRoutesItemsAndErrorsThenStopsOnTerminal(t *testing.T) {
	ch := make(chan Event[int], 4)
	ch <- Event[int]{Item: 1}
	ch <- Event[int]{Err: errors.New("boom")}
	ch <- Event[int]{Item: 2, Terminal: true}
	ch <- Event[int]{Item: 99} // after terminal — must be ignored
	src := &fakeSource[int]{ch: ch}

	var mu sync.Mutex
	var items []int
	var errs []string
	Pump(context.Background(), src,
		func(m tea.Msg) {
			mu.Lock()
			defer mu.Unlock()
			switch v := m.(type) {
			case int:
				items = append(items, v)
			case string:
				errs = append(errs, v)
			}
		},
		func(i int) tea.Msg { return i },
		func(e error) tea.Msg { return e.Error() },
	)

	// 1 routes, then the terminal event's item (2) routes, then Pump
	// stops — so 99 (queued after terminal) is never delivered.
	if len(items) != 2 || items[0] != 1 || items[1] != 2 {
		t.Fatalf("items = %v, want [1 2] (99 after terminal ignored)", items)
	}
	if len(errs) != 1 || errs[0] != "boom" {
		t.Fatalf("errs = %v, want [boom]", errs)
	}
}

func TestPumpStopsOnContextCancel(t *testing.T) {
	src := &fakeSource[int]{ch: make(chan Event[int])} // never sends
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Pump(ctx, src, func(tea.Msg) {}, func(i int) tea.Msg { return i }, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pump did not return on ctx cancel")
	}
}

// fakeBus satisfies SubscribeBus.
type fakeBus struct {
	ch  chan client.Message
	err error
}

func (f *fakeBus) Subscribe(_ context.Context, _ string, _ ...client.SubscribeOption) (<-chan client.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ch, nil
}

func TestSubscribeSourceForwardsAndCloses(t *testing.T) {
	bus := &fakeBus{ch: make(chan client.Message, 1)}
	src := SubscribeSource(context.Background(), bus, "subj")
	bus.ch <- client.Message{Subject: "subj"}
	select {
	case ev := <-src.Events():
		if ev.Err != nil || ev.Item.Subject != "subj" {
			t.Fatalf("event = %+v, want item subj", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event forwarded")
	}
	close(bus.ch)
	select {
	case _, ok := <-src.Events():
		if ok {
			t.Fatal("source channel should close when bus closes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("source did not close after bus closed")
	}
}

func TestSubscribeSourceEmitsTerminalOnSubscribeError(t *testing.T) {
	bus := &fakeBus{err: errors.New("no daemon")}
	src := SubscribeSource(context.Background(), bus, "subj")
	ev := <-src.Events()
	if ev.Err == nil || !ev.Terminal {
		t.Fatalf("event = %+v, want terminal error", ev)
	}
}

func TestTailSourceReplaysExistingLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(path, []byte("alpha\nbravo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := TailSource(path)
	defer func() { _ = src.Close() }()

	got := make([]string, 0, 2)
	for len(got) < 2 {
		select {
		case ev := <-src.Events():
			if ev.Err != nil {
				t.Fatalf("tail error: %v", ev.Err)
			}
			got = append(got, ev.Item)
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out; got %v", got)
		}
	}
	if got[0] != "alpha" || got[1] != "bravo" {
		t.Fatalf("tailed %v, want [alpha bravo]", got)
	}
}
