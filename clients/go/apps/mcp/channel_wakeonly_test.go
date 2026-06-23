package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// setWakeOnly sets SEXTANT_MCP_WAKE_ONLY=1 for the duration of t and restores
// the original value on cleanup.
func setWakeOnly(t *testing.T) {
	t.Helper()
	t.Setenv("SEXTANT_MCP_WAKE_ONLY", "1")
}

// clearWakeOnly ensures SEXTANT_MCP_WAKE_ONLY is unset for the duration of t.
func clearWakeOnly(t *testing.T) {
	t.Helper()
	t.Setenv("SEXTANT_MCP_WAKE_ONLY", "")
}

// TestWakeOnlyModeEnvToggle proves that wakeOnlyMode() returns true only when
// SEXTANT_MCP_WAKE_ONLY=1.
func TestWakeOnlyModeEnvToggle(t *testing.T) {
	clearWakeOnly(t)
	if wakeOnlyMode() {
		t.Error("wakeOnlyMode() = true with empty env var, want false")
	}

	setWakeOnly(t)
	if !wakeOnlyMode() {
		t.Error("wakeOnlyMode() = false with SEXTANT_MCP_WAKE_ONLY=1, want true")
	}
}

// TestWakeOnlyFrameEventNoBody proves that in WAKE_ONLY mode, frameEvent
// pushes a notification that does NOT contain the message body or record text,
// and DOES carry the subject and a wake marker.
func TestWakeOnlyFrameEventNoBody(t *testing.T) {
	setWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01A": "alice"}))

	const bodyText = "secret principal message"
	const record = `{"$type":"chat.message","text":"` + bodyText + `"}`
	h.frameEvent(msg("msg.topic.plan", "01A", record, 5))

	content, meta := rec.last(t)

	// Content must NOT contain the message body.
	if strings.Contains(content, bodyText) {
		t.Errorf("wake-only content %q must not contain the message body %q", content, bodyText)
	}
	if strings.Contains(content, record) {
		t.Errorf("wake-only content %q must not contain the raw record %q", content, record)
	}

	// Content must be valid JSON with wake=true.
	var wake map[string]any
	if err := json.Unmarshal([]byte(content), &wake); err != nil {
		t.Fatalf("wake-only content is not valid JSON: %v (got %q)", err, content)
	}
	if wake["wake"] != true {
		t.Errorf("wake[\"wake\"] = %v, want true", wake["wake"])
	}
	if wake["subject"] != "msg.topic.plan" {
		t.Errorf("wake[\"subject\"] = %v, want msg.topic.plan", wake["subject"])
	}

	// Meta must carry subject, seq, id, and wake marker.
	if meta["subject"] != "msg.topic.plan" {
		t.Errorf("meta[\"subject\"] = %v, want msg.topic.plan", meta["subject"])
	}
	if meta["seq"] != "5" {
		t.Errorf("meta[\"seq\"] = %v, want \"5\"", meta["seq"])
	}
	if meta["id"] == "" {
		t.Error("meta[\"id\"] must be set in wake-only mode")
	}
	if meta["wake"] != "1" {
		t.Errorf("meta[\"wake\"] = %v, want \"1\"", meta["wake"])
	}

	// Meta must NOT carry sender or sender_id (message body is not delivered).
	if _, ok := meta["sender"]; ok {
		t.Error("wake-only meta must not carry sender")
	}
	if _, ok := meta["sender_id"]; ok {
		t.Error("wake-only meta must not carry sender_id")
	}

	// All meta keys must be alphanumeric+underscore (harness requirement).
	for k := range meta {
		if !metaKeyRE.MatchString(k) {
			t.Errorf("meta key %q not alphanumeric+underscore — the harness drops it silently", k)
		}
	}
}

// TestContentModeFrameEventPushesBody proves that in CONTENT mode (default,
// SEXTANT_MCP_WAKE_ONLY unset), frameEvent pushes the message body as before.
func TestContentModeFrameEventPushesBody(t *testing.T) {
	clearWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01A": "alice"}))

	h.frameEvent(msg("msg.topic.plan", "01A", `{"$type":"chat.message","text":"hello world"}`, 3))

	content, meta := rec.last(t)
	if content != "hello world" {
		t.Errorf("content mode content = %q, want the chat.message text", content)
	}
	if meta["sender"] != "alice" {
		t.Errorf("content mode meta[\"sender\"] = %v, want alice", meta["sender"])
	}
	if meta["sender_id"] != "01A" {
		t.Errorf("content mode meta[\"sender_id\"] = %v, want 01A", meta["sender_id"])
	}
}

// TestWakeOnlySelfEchoSuppressed proves that self-echo suppression fires BEFORE
// the wake/content branch: a self-published frame produces NO push in wake-only
// mode, just as in content mode.
func TestWakeOnlySelfEchoSuppressed(t *testing.T) {
	setWakeOnly(t)

	rec := &recorder{}
	h := newChannelHub(rec.notify, staticNames(map[string]string{"01A": "alice"}))

	const echoID = "01WAKE_ECHO_ID"
	h.echo.record(echoID)

	h.frameEvent(msgID(echoID, "msg.topic.plan", "01A", `{"$type":"chat.message","text":"my own wake msg"}`, 9))

	rec.mu.Lock()
	n := len(rec.events)
	rec.mu.Unlock()
	if n != 0 {
		t.Errorf("self-echo in wake-only mode delivered %d event(s), want 0", n)
	}
}
