package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type fakeBus struct {
	sent     []string
	restarted []uuid.UUID
}

func (f *fakeBus) SendPrompt(_ context.Context, _ uuid.UUID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeBus) RestartAgent(_ context.Context, id uuid.UUID) error {
	f.restarted = append(f.restarted, id)
	return nil
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
