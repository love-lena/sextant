package sextantproto

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// updateCorpus regenerates the checked-in wire corpus instead of asserting
// against it. Run `go test ./pkg/sextantproto/ -run TestWireCorpus
// -update-corpus` after a deliberate wire change, then commit the result.
var updateCorpus = flag.Bool("update-corpus", false, "rewrite the checked-in wire corpus under testdata/wire-corpus")

const corpusDir = "testdata/wire-corpus"

// fixedUUID returns a deterministic UUID from a single byte so the corpus
// is stable across regenerations (no random IDs in committed fixtures).
func fixedUUID(b byte) uuid.UUID {
	var u uuid.UUID
	for i := range u {
		u[i] = b
	}
	return u
}

func fixedTime() Timestamp {
	return AtTimestamp(time.Date(2026, 5, 29, 12, 34, 56, 789000*1000, time.UTC))
}

// corpusEnvelope builds one canonical envelope for the given kind with a
// fully-populated, deterministic payload. The set spans every envelope
// Kind so the Go↔TS round-trip exercises the whole wire surface.
func corpusEnvelopes(t *testing.T) map[string]Envelope {
	t.Helper()
	from := Address{Kind: AddressAgent, ID: fixedUUID(0x11).String()}
	op := Address{Kind: AddressOperator, ID: "lena"}

	mk := func(kind Kind, fromAddr Address, payload any) Envelope {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload for %s: %v", kind, err)
		}
		return Envelope{
			ID:           fixedUUID(0x01),
			Ts:           fixedTime(),
			ProtoVersion: ProtoVersion,
			From:         fromAddr,
			TraceID:      fixedUUID(0x01),
			SpanID:       fixedUUID(0x02),
			Kind:         kind,
			Payload:      raw,
		}
	}

	exit := 0
	return map[string]Envelope{
		"agent_frame": mk(KindAgentFrame, from, AgentFramePayload{
			FrameKind: FrameToolCall,
			SessionID: "sess-1",
			ToolName:  "read_file",
			Body:      map[string]any{"path": "README.md"},
			Tokens:    &FrameTokens{Input: 1000, Output: 200, CacheRead: 50},
			Tags:      map[string]string{"agent": "lead"},
		}),
		"lifecycle": mk(KindLifecycle, from, LifecyclePayload{
			IncarnationID: fixedUUID(0x21),
			AgentUUID:     fixedUUID(0x11),
			Transition:    LifecycleEnded,
			State:         IncarnationExited,
			Reason:        "clean exit",
			ExitCode:      &exit,
			Source:        LifecycleSourceReconciler,
		}),
		"audit": mk(KindAudit, op, AuditPayload{
			Actor:              "lena",
			AgentUUID:          func() *uuid.UUID { u := fixedUUID(0x11); return &u }(),
			Action:             "spawn_agent",
			CapabilityRequired: "agent.spawn",
			Result:             AuditAllowed,
			Details:            map[string]any{"name": "lead"},
		}),
		"user_input_request": mk(KindUserInputRequest, from, UserInputRequestPayload{
			RequestID: fixedUUID(0x31),
			FromUUID:  fixedUUID(0x11),
			Question:  "approve?",
			Options:   []string{"yes", "no"},
			Urgency:   "high",
		}),
		"user_input_response": mk(KindUserInputResponse, op, UserInputResponsePayload{
			RequestID: fixedUUID(0x31),
			Decision:  InputAnswer,
			Answer:    "yes",
		}),
		"heartbeat": mk(KindHeartbeat, from, HeartbeatPayload{
			AgentUUID:      fixedUUID(0x11),
			IncarnationID:  fixedUUID(0x21),
			HostID:         "host-1",
			UptimeSeconds:  42,
			PendingPrompts: 1,
		}),
	}
}

// TestWireCorpus is the authoritative Go side of the Go↔TS round-trip. It
// writes (with -update-corpus) or verifies a checked-in corpus of
// canonical envelopes. The TS suite
// (clients/typescript/test/wire-corpus.test.ts) reads the same files and
// asserts the generated types decode them — the cross-language contract
// test the C1 ticket requires.
func TestWireCorpus(t *testing.T) {
	envs := corpusEnvelopes(t)

	names := make([]string, 0, len(envs))
	for name := range envs {
		names = append(names, name)
	}
	sort.Strings(names)

	if *updateCorpus {
		if err := os.MkdirAll(corpusDir, 0o755); err != nil {
			t.Fatalf("mkdir corpus: %v", err)
		}
		for _, name := range names {
			raw, err := json.MarshalIndent(envs[name], "", "  ")
			if err != nil {
				t.Fatalf("marshal %s: %v", name, err)
			}
			raw = append(raw, '\n')
			if err := os.WriteFile(filepath.Join(corpusDir, name+".json"), raw, 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		t.Logf("wrote %d corpus files to %s", len(names), corpusDir)
		return
	}

	for _, name := range names {
		path := filepath.Join(corpusDir, name+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read corpus %s (run with -update-corpus to regenerate): %v", path, err)
		}

		// Decode → validate → re-encode round-trips structurally.
		var got Envelope
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal corpus %s: %v", name, err)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("corpus %s fails Validate: %v", name, err)
		}
		if string(got.Kind) != name {
			t.Errorf("corpus %s has kind %q, want %q", name, got.Kind, name)
		}

		// The committed bytes must equal a fresh marshal of the canonical
		// envelope — guards against the corpus drifting from the structs.
		want, err := json.MarshalIndent(envs[name], "", "  ")
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		want = append(want, '\n')
		if string(raw) != string(want) {
			t.Errorf("corpus %s out of date; run `go test ./pkg/sextantproto/ -run TestWireCorpus -update-corpus`", name)
		}
	}
}
