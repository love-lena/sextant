package agentactivity_test

import (
	"encoding/json"
	"testing"

	agentactivity "github.com/love-lena/sextant/conventions/agentactivity/go"
)

func TestActivitySubject(t *testing.T) {
	got := agentactivity.ActivitySubject("01H")
	want := "msg.agent.01H.activity"
	if got != want {
		t.Fatalf("ActivitySubject(01H) = %q, want %q", got, want)
	}
}

// A turn_end record round-trips through Marshal/ParseActivity unchanged — the
// turn_end kind is the rest signal the run executor (TASK-236) consumes.
func TestRoundTripTurnEnd(t *testing.T) {
	in := agentactivity.Activity{Kind: agentactivity.KindTurnEnd, TurnIndex: 3, Updated: "2026-06-26T00:00:00Z"}
	got, ok := agentactivity.ParseActivity(in.Marshal())
	if !ok {
		t.Fatal("ParseActivity rejected a record Marshal produced")
	}
	if got.Type != agentactivity.KindActivity {
		t.Errorf("$type = %q, want %q", got.Type, agentactivity.KindActivity)
	}
	if got.Kind != agentactivity.KindTurnEnd || got.TurnIndex != 3 || got.Updated != in.Updated {
		t.Errorf("round-trip lost fields: %+v", got)
	}
}

// ParseActivity rejects a record whose $type is not agent.activity — a reader
// must not mistake another convention's record on a shared subscription for one.
func TestParseRejectsWrongType(t *testing.T) {
	other := json.RawMessage(`{"$type":"goal.update","kind":"turn_end"}`)
	if _, ok := agentactivity.ParseActivity(other); ok {
		t.Error("ParseActivity accepted a non-agent.activity record")
	}
	if _, ok := agentactivity.ParseActivity(json.RawMessage(`not json`)); ok {
		t.Error("ParseActivity accepted malformed JSON")
	}
}
