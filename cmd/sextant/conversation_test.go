package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestStreamConversationRendersAssistantText feeds one assistant_text
// frame through the streaming core and asserts the rendered output
// carries the body text.
func TestStreamConversationRendersAssistantText(t *testing.T) {
	frames := make(chan client.Message, 1)
	frames <- client.Message{
		Envelope: buildFrameEnvelope(t, sextantproto.AgentFramePayload{
			FrameKind: sextantproto.FrameAssistantText,
			Body:      map[string]any{"text": "hello, operator"},
		}),
		Timestamp: time.Now(),
		Ack:       func() error { return nil },
	}
	close(frames)

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := streamConversation(ctx, &buf, frames, nil, uuid.New(), false, false); err != nil {
		t.Fatalf("streamConversation: %v", err)
	}
	if !strings.Contains(buf.String(), "hello, operator") {
		t.Errorf("output missing text: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "[assistant]") {
		t.Errorf("output missing [assistant] label: %q", buf.String())
	}
}

// TestStreamConversationJSONEmitsNDJSON feeds one frame through
// streamConversation with asJSON=true and asserts one JSON object
// per line lands on the writer.
func TestStreamConversationJSONEmitsNDJSON(t *testing.T) {
	frames := make(chan client.Message, 2)
	frames <- client.Message{
		Envelope: buildFrameEnvelope(t, sextantproto.AgentFramePayload{
			FrameKind: sextantproto.FrameAssistantText,
			Body:      map[string]any{"text": "first"},
		}),
		Ack: func() error { return nil },
	}
	frames <- client.Message{
		Envelope: buildFrameEnvelope(t, sextantproto.AgentFramePayload{
			FrameKind: sextantproto.FrameToolCall,
			ToolName:  "list_agents",
			Body:      map[string]any{},
		}),
		Ack: func() error { return nil },
	}
	close(frames)

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := streamConversation(ctx, &buf, frames, nil, uuid.New(), true, false); err != nil {
		t.Fatalf("streamConversation: %v", err)
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

// TestStreamConversationTailExitsOnLifecycleEnded proves that --tail
// causes streamConversation to return when a lifecycle.ended for
// the targeted agent arrives.
func TestStreamConversationTailExitsOnLifecycleEnded(t *testing.T) {
	frames := make(chan client.Message)
	lifecycle := make(chan client.Message, 1)
	agentID := uuid.New()

	// Push the lifecycle.ended envelope.
	payload, _ := json.Marshal(sextantproto.LifecyclePayload{
		AgentUUID:  agentID,
		Transition: sextantproto.LifecycleEnded,
		State:      sextantproto.IncarnationExited,
	})
	lifecycle <- client.Message{
		Envelope: sextantproto.NewEnvelope(sextantproto.KindLifecycle,
			sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "daemon"},
			payload),
		Ack: func() error { return nil },
	}

	// streamConversation must exit on the lifecycle.ended even though
	// the frames channel hasn't closed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := streamConversation(ctx, &buf, frames, lifecycle, agentID, false, true); err != nil {
		t.Fatalf("streamConversation: %v", err)
	}
	if !strings.Contains(buf.String(), "lifecycle: ended") {
		t.Errorf("output missing lifecycle marker: %q", buf.String())
	}
}

// buildFrameEnvelope wraps the AgentFramePayload into a valid
// envelope. Reused by multiple tests.
func buildFrameEnvelope(t *testing.T, fp sextantproto.AgentFramePayload) sextantproto.Envelope {
	t.Helper()
	raw, err := json.Marshal(fp)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return sextantproto.NewEnvelope(sextantproto.KindAgentFrame,
		sextantproto.Address{Kind: sextantproto.AddressAgent, ID: uuid.NewString()}, raw)
}
