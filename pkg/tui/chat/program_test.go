package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeBus struct {
	sent       []string
	restarted  []uuid.UUID
	restartErr error
}

func (f *fakeBus) SendPrompt(_ context.Context, _ uuid.UUID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeBus) RestartAgent(_ context.Context, id uuid.UUID) error {
	f.restarted = append(f.restarted, id)
	return f.restartErr
}

func TestSendHookCallsBusSendPrompt(t *testing.T) {
	t.Parallel()
	bus := &fakeBus{}
	id := uuid.New()
	hook := makeSendHook(context.Background(), bus, id)
	hook("hello world")
	if len(bus.sent) != 1 || bus.sent[0] != "hello world" {
		t.Errorf("bus sent: %v", bus.sent)
	}
}

// TestRestartHookEmitsFailedMsgOnRPCError exercises the program.go seam
// for feat-tui-chat-restart-error-banner: a Bus.RestartAgent error must
// surface as a restartFailedMsg carrying the error string so the chat
// reducer can render an inline banner. Success path returns nil so no
// banner appears when the daemon accepts the call (the watcher's
// "restarted" envelope handles the success UX).
func TestRestartHookEmitsFailedMsgOnRPCError(t *testing.T) {
	t.Parallel()
	bus := &fakeBus{restartErr: errors.New("daemon unreachable")}
	id := uuid.New()
	hook := makeRestartHook(context.Background(), bus)
	cmd := hook(id)
	if cmd == nil {
		t.Fatal("hook returned nil cmd; want a tea.Cmd")
	}
	msg := cmd()
	failed, ok := msg.(restartFailedMsg)
	if !ok {
		t.Fatalf("cmd msg type = %T, want restartFailedMsg", msg)
	}
	if failed.Err != "daemon unreachable" {
		t.Errorf("restartFailedMsg.Err = %q, want %q", failed.Err, "daemon unreachable")
	}
	if len(bus.restarted) != 1 || bus.restarted[0] != id {
		t.Errorf("bus.restarted = %v, want [%s]", bus.restarted, id)
	}
}

// TestRestartHookReturnsNilOnSuccess pins the success branch: a clean
// restart_agent RPC must NOT emit a banner-bearing message.
func TestRestartHookReturnsNilOnSuccess(t *testing.T) {
	t.Parallel()
	bus := &fakeBus{}
	hook := makeRestartHook(context.Background(), bus)
	cmd := hook(uuid.New())
	if cmd == nil {
		t.Fatal("hook returned nil cmd; want a tea.Cmd")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("cmd produced msg %T (%v) on success; want nil", msg, msg)
	}
}
