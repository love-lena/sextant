package main

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
		Type:     typeSpawnRequest,
		Prompt:   "say hello on msg.topic.demo",
		Nickname: "alpha",
		Job:      "job-7",
		Parent:   "01PARENT",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, ok := parseSpawnRequest(b)
	if !ok {
		t.Fatalf("parseSpawnRequest returned false for a valid request")
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
		if _, ok := parseSpawnRequest(json.RawMessage(rec)); ok {
			t.Errorf("%s: parseSpawnRequest accepted a record it should reject", name)
		}
	}
}

// TestSpawnAckMarshal covers the ack side: marshal stamps $type and the lineage
// round-trips, and a failed spawn carries status=error with detail.
func TestSpawnAckMarshal(t *testing.T) {
	ack := SpawnAck{ID: "01CHILD", Nickname: "alpha", RequestID: "01REQ", Job: "job-7", Parent: "01PARENT", Status: statusOK}
	var got SpawnAck
	if err := json.Unmarshal(ack.marshal(), &got); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if got.Type != typeSpawnAck {
		t.Errorf("ack $type = %q, want %q", got.Type, typeSpawnAck)
	}
	if got.ID != "01CHILD" || got.RequestID != "01REQ" || got.Job != "job-7" || got.Parent != "01PARENT" || got.Status != statusOK {
		t.Errorf("ack round-trip mismatch: %+v", got)
	}

	fail := SpawnAck{RequestID: "01REQ", Status: statusError, Error: "mint child: boom"}
	var gotFail SpawnAck
	if err := json.Unmarshal(fail.marshal(), &gotFail); err != nil {
		t.Fatalf("unmarshal fail ack: %v", err)
	}
	if gotFail.Status != statusError || gotFail.Error == "" || gotFail.ID != "" {
		t.Errorf("error ack should carry status=error + detail and no id: %+v", gotFail)
	}
}

// TestSpawnLexiconsParse covers AC#1's contract artifact: the spawn lexicon JSON
// files exist, parse, and declare the expected ids. They are ordinary message
// records (no wire/epoch surface), so this is the whole conformance bar.
func TestSpawnLexiconsParse(t *testing.T) {
	for _, want := range []string{typeSpawnRequest, typeSpawnAck} {
		path := filepath.Join("..", "..", "..", "..", "protocol", "lexicons", want+".json")
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
