package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestStreamAskTurnRendersAssistantReplyAndExits feeds one assistant
// frame plus a turn_ended lifecycle envelope through streamAskTurn and
// asserts it renders both and returns nil (clean exit). This is the
// unit-level shape of the issue spec's `TestAskRendersAssistantReplyAndExits`.
func TestStreamAskTurnRendersAssistantReplyAndExits(t *testing.T) {
	agentID := uuid.New()
	frames := make(chan client.Message, 1)
	lifecycle := make(chan client.Message, 1)

	frames <- client.Message{
		Envelope: buildFrameEnvelope(t, sextantproto.AgentFramePayload{
			FrameKind: sextantproto.FrameAssistantText,
			Body:      map[string]any{"text": "ack"},
		}),
		Ack: func() error { return nil },
	}
	turnEnded, err := json.Marshal(sextantproto.LifecyclePayload{
		AgentUUID:  agentID,
		Transition: sextantproto.LifecycleTurnEnded,
		State:      sextantproto.IncarnationState("running"),
	})
	if err != nil {
		t.Fatalf("marshal turn_ended: %v", err)
	}
	lifecycle <- client.Message{
		Envelope: sextantproto.NewEnvelope(sextantproto.KindLifecycle,
			sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()},
			turnEnded),
		Ack: func() error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := streamAskTurn(ctx, &buf, frames, lifecycle, agentID, false, 5*time.Second); err != nil {
		t.Fatalf("streamAskTurn: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[assistant]") {
		t.Errorf("output missing [assistant] label: %q", out)
	}
	if !strings.Contains(out, "ack") {
		t.Errorf("output missing assistant text 'ack': %q", out)
	}
	if !strings.Contains(out, "transition=turn_ended") {
		t.Errorf("output missing transition=turn_ended: %q", out)
	}
}

// TestStreamAskTurnExitsOnLifecycleEnded verifies that a session-end
// lifecycle (transition=ended) also terminates the wait — not just
// turn_ended. Spec says: "Exit cleanly on lifecycle transition=turn_ended
// OR transition=ended".
func TestStreamAskTurnExitsOnLifecycleEnded(t *testing.T) {
	agentID := uuid.New()
	frames := make(chan client.Message)
	lifecycle := make(chan client.Message, 1)

	ended, err := json.Marshal(sextantproto.LifecyclePayload{
		AgentUUID:  agentID,
		Transition: sextantproto.LifecycleEnded,
		State:      sextantproto.IncarnationExited,
	})
	if err != nil {
		t.Fatalf("marshal ended: %v", err)
	}
	lifecycle <- client.Message{
		Envelope: sextantproto.NewEnvelope(sextantproto.KindLifecycle,
			sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()},
			ended),
		Ack: func() error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := streamAskTurn(ctx, &buf, frames, lifecycle, agentID, false, 5*time.Second); err != nil {
		t.Fatalf("streamAskTurn: %v", err)
	}
	if !strings.Contains(buf.String(), "transition=ended") {
		t.Errorf("output missing transition=ended: %q", buf.String())
	}
}

// TestStreamAskTurnTimeoutExitsWithError feeds nothing and asserts that
// after --timeout streamAskTurn returns a non-nil error whose message
// makes the timeout cause obvious. Mirrors the issue's
// `TestAskTimeoutExits` acceptance test.
func TestStreamAskTurnTimeoutExitsWithError(t *testing.T) {
	agentID := uuid.New()
	frames := make(chan client.Message)
	lifecycle := make(chan client.Message)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	start := time.Now()
	err := streamAskTurn(ctx, &buf, frames, lifecycle, agentID, false, 200*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("streamAskTurn returned nil, expected timeout error")
	}
	if !errors.Is(err, errAskTimeout) {
		t.Errorf("error not errAskTimeout: %v", err)
	}
	if !strings.Contains(err.Error(), "turn_ended") {
		t.Errorf("error message should mention turn_ended: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestStreamAskTurnJSONEmitsNDJSON verifies --json mode emits one
// envelope per line (NDJSON), same as conversation --json. Both the
// frame and the lifecycle envelope land as raw JSON.
func TestStreamAskTurnJSONEmitsNDJSON(t *testing.T) {
	agentID := uuid.New()
	frames := make(chan client.Message, 1)
	lifecycle := make(chan client.Message, 1)

	frames <- client.Message{
		Envelope: buildFrameEnvelope(t, sextantproto.AgentFramePayload{
			FrameKind: sextantproto.FrameAssistantText,
			Body:      map[string]any{"text": "ack"},
		}),
		Ack: func() error { return nil },
	}
	turnEnded, err := json.Marshal(sextantproto.LifecyclePayload{
		AgentUUID:  agentID,
		Transition: sextantproto.LifecycleTurnEnded,
		State:      sextantproto.IncarnationState("running"),
	})
	if err != nil {
		t.Fatalf("marshal turn_ended: %v", err)
	}
	lifecycle <- client.Message{
		Envelope: sextantproto.NewEnvelope(sextantproto.KindLifecycle,
			sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()},
			turnEnded),
		Ack: func() error { return nil },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := streamAskTurn(ctx, &buf, frames, lifecycle, agentID, true, 5*time.Second); err != nil {
		t.Fatalf("streamAskTurn: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("NDJSON line count = %d, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var env sextantproto.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d not JSON: %v (%q)", i, err, line)
		}
	}
}
