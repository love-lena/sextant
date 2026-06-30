package spawn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSpawnRequestRoundTrip covers AC#1: the spawn.request message kind, with
// job/parent lineage, marshals and parses back intact.
func TestSpawnRequestRoundTrip(t *testing.T) {
	in := SpawnRequest{
		Type:     TypeSpawnRequest,
		Prompt:   "say hello on msg.topic.demo",
		Nickname: "alpha",
		Job:      "job-7",
		Parent:   "01PARENT",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, ok := ParseSpawnRequest(b)
	if !ok {
		t.Fatalf("ParseSpawnRequest returned false for a valid request")
	}
	if got != in {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, in)
	}
}

// TestParseSpawnRequestRejects covers the dispatcher's guard: it acts only on
// well-formed spawn.request records, ignoring its own echoed spawn.ack, other
// $types, and a request with no prompt.
func TestParseSpawnRequestRejects(t *testing.T) {
	cases := map[string]string{
		"spawn.ack echoed back": `{"$type":"spawn.ack","id":"01X","requestId":"01R","status":"ok"}`,
		"chat.message":          `{"$type":"chat.message","text":"hi"}`,
		"missing prompt":        `{"$type":"spawn.request","nickname":"alpha"}`,
		"empty object":          `{}`,
		"not json":              `{`,
	}
	for name, rec := range cases {
		if _, ok := ParseSpawnRequest(json.RawMessage(rec)); ok {
			t.Errorf("%s: ParseSpawnRequest accepted a record it should reject", name)
		}
	}
}

// TestSpawnAckMarshal covers the ack side: marshal stamps $type and the lineage
// round-trips, and a failed spawn carries status=error with detail.
func TestSpawnAckMarshal(t *testing.T) {
	ack := SpawnAck{ID: "01CHILD", Nickname: "alpha", RequestID: "01REQ", Job: "job-7", Parent: "01PARENT", Status: StatusOK}
	var got SpawnAck
	if err := json.Unmarshal(ack.Marshal(), &got); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if got.Type != TypeSpawnAck {
		t.Errorf("ack $type = %q, want %q", got.Type, TypeSpawnAck)
	}
	if got.ID != "01CHILD" || got.RequestID != "01REQ" || got.Job != "job-7" || got.Parent != "01PARENT" || got.Status != StatusOK {
		t.Errorf("ack round-trip mismatch: %+v", got)
	}

	fail := SpawnAck{RequestID: "01REQ", Status: StatusError, Error: "mint child: boom"}
	var gotFail SpawnAck
	if err := json.Unmarshal(fail.Marshal(), &gotFail); err != nil {
		t.Fatalf("unmarshal fail ack: %v", err)
	}
	if gotFail.Status != StatusError || gotFail.Error == "" || gotFail.ID != "" {
		t.Errorf("error ack should carry status=error + detail and no id: %+v", gotFail)
	}
}

// TestSpawnLexiconsParse covers AC#1's contract artifact: the spawn lexicon JSON
// files exist, parse, and declare the expected ids. They are ordinary message
// records (no wire/epoch surface), so this is the whole conformance bar.
func TestSpawnLexiconsParse(t *testing.T) {
	for _, want := range []string{TypeSpawnRequest, TypeSpawnAck} {
		path := filepath.Join("..", "..", "..", "protocol", "lexicons", want+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var lex struct {
			Lexicon int            `json:"lexicon"`
			ID      string         `json:"id"`
			Defs    map[string]any `json:"defs"`
		}
		if err := json.Unmarshal(b, &lex); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if lex.ID != want {
			t.Errorf("%s: id = %q, want %q", path, lex.ID, want)
		}
		if _, ok := lex.Defs["main"]; !ok {
			t.Errorf("%s: missing defs.main", path)
		}
	}
}

// TestSpawnRequestModelField pins TASK-245 at the spawn-convention level: the
// Model field carried by a spawn.request is preserved through marshal/unmarshal,
// and a request with no Model omits the key entirely (omitempty).
func TestSpawnRequestModelField(t *testing.T) {
	// With a declared model: the key must survive the round-trip.
	req := SpawnRequest{
		Type:   TypeSpawnRequest,
		Prompt: "write a draft",
		Model:  "claude-opus-4-5",
	}
	b := SpawnRequestRecord(req)
	got, ok := ParseSpawnRequest(b)
	if !ok {
		t.Fatal("ParseSpawnRequest rejected a request with Model set")
	}
	if got.Model != "claude-opus-4-5" {
		t.Errorf("Model not preserved: got %q, want %q", got.Model, "claude-opus-4-5")
	}
	// Wire shape: the key must be "model" (matches the dispatcher's JSON tag).
	raw := string(b)
	if !func() bool {
		for i := 0; i+len(`"model"`) <= len(raw); i++ {
			if raw[i:i+len(`"model"`)] == `"model"` {
				return true
			}
		}
		return false
	}() {
		t.Errorf("SpawnRequest with Model does not emit \"model\" key: %s", raw)
	}

	// Without a declared model: the key must be absent (omitempty).
	noModel := SpawnRequest{Type: TypeSpawnRequest, Prompt: "write a draft"}
	nb := SpawnRequestRecord(noModel)
	noRaw := string(nb)
	if func() bool {
		for i := 0; i+len(`"model"`) <= len(noRaw); i++ {
			if noRaw[i:i+len(`"model"`)] == `"model"` {
				return true
			}
		}
		return false
	}() {
		t.Errorf("SpawnRequest with no Model emitted \"model\" key (must be omitted): %s", nb)
	}
}
