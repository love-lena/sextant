package workflow

import (
	"encoding/json"
	"testing"
)

// TestSpawnAckParse covers the M5.2-composition correlation: a spawn.ack parses and
// a spawn.request (or other $type) is rejected.
func TestSpawnAckParse(t *testing.T) {
	ack := json.RawMessage(`{"$type":"spawn.ack","id":"01CHILD","requestId":"01REQ","status":"ok"}`)
	if got, ok := ParseSpawnAck(ack); !ok || got.ID != "01CHILD" || got.RequestID != "01REQ" || got.Status != "ok" {
		t.Errorf("spawn.ack parse: ok=%v got=%+v", ok, got)
	}
	if _, ok := ParseSpawnAck(json.RawMessage(`{"$type":"spawn.request","prompt":"x"}`)); ok {
		t.Error("ParseSpawnAck accepted a spawn.request")
	}
}
