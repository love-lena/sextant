package main

import (
	"context"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/client"
)

// TestEventsTailRegistersForFlag covers `feat-sextant-tail-duration-flag.md`:
// `sextant events tail --for <duration>` exists, is a Duration, and the
// `--from-seq` companion is still wired.
func TestEventsTailRegistersForFlag(t *testing.T) {
	cmd := newEventsTailCmd()

	forFlag := cmd.Flags().Lookup("for")
	if forFlag == nil {
		t.Fatalf("--for flag is not registered on `events tail`")
	}
	if forFlag.Value.Type() != "duration" {
		t.Errorf("--for flag type = %q, want duration", forFlag.Value.Type())
	}
	if forFlag.DefValue != "0s" {
		t.Errorf("--for default = %q, want 0s (run until interrupted)", forFlag.DefValue)
	}

	if cmd.Flags().Lookup("from-seq") == nil {
		t.Errorf("--from-seq companion regressed")
	}
}

// TestStreamTailExitsWhenContextDeadlineFires covers the runtime
// invariant: once the context fires (Ctrl-C OR --for deadline), the
// stream loop returns nil without consuming any further messages. The
// --for plumbing in runEventsTail just wraps ctx with WithTimeout; the
// behavior under deadline is governed by this loop.
func TestStreamTailExitsWhenContextDeadlineFires(t *testing.T) {
	ch := make(chan client.Message) // never written to
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- streamTail(ctx, &discardWriter{}, ch, false) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("streamTail err = %v, want nil on deadline", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("streamTail did not return within 500ms of a 50ms deadline")
	}
}

type discardWriter struct{}

func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
