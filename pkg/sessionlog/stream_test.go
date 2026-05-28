package sessionlog_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sessionlog"
)

// TestStreamFixture parses pkg/sessionlog/testdata/session.jsonl
// front-to-back and verifies every record-type shape we model. The
// fixture is curated to exercise:
//
//   - a plain-string user prompt
//   - an assistant message with thinking + text + tool_use blocks
//     and a usage payload with 5m/1h cache tiers
//   - a user tool_result with a string content body
//   - a user tool_result with an array content body (sub-blocks)
//   - an assistant message with isSidechain=true (subagent)
//   - a system record (subtype + level)
//   - five unmodeled "metadata" types (mode / queue-operation /
//     ai-title / attachment / unknown-novel-type) that must fall
//     through to RawEvent without aborting the stream
//   - one syntactically broken line — must produce a RawEvent with
//     ParseError set and NOT halt the stream
//   - one assistant whose content includes an unknown block type
//     (server_tool_call) — must produce a RawBlock
//
// The assertions below count event kinds and probe one representative
// of each shape, rather than asserting field-by-field on every line
// (that would duplicate the fixture and obscure intent).
func TestStreamFixture(t *testing.T) {
	t.Parallel()

	f, err := os.Open("testdata/session.jsonl")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	var events []sessionlog.Event
	for ev := range sessionlog.Stream(f) {
		events = append(events, ev)
	}
	if got, want := len(events), 13; got != want {
		t.Fatalf("expected %d events from fixture, got %d", want, got)
	}

	// Spot-check kinds index-by-index against the curated fixture
	// order. If the fixture is ever reordered, this loop becomes the
	// canonical map.
	expectedKinds := []string{
		sessionlog.KindUser,      // 0 plain-string prompt
		sessionlog.KindAssistant, // 1 thinking + text + tool_use
		sessionlog.KindUser,      // 2 tool_result string
		sessionlog.KindUser,      // 3 tool_result array
		sessionlog.KindAssistant, // 4 sidechain
		sessionlog.KindSystem,    // 5 system record
		"mode",                   // 6 metadata RawEvent
		"queue-operation",        // 7
		"ai-title",               // 8
		"attachment",             // 9
		"unknown-novel-type",     // 10
		"",                       // 11 broken JSON → empty kind
		sessionlog.KindAssistant, // 12 unknown block type
	}
	for i, want := range expectedKinds {
		if got := events[i].Kind(); got != want {
			t.Errorf("event[%d] kind = %q, want %q", i, got, want)
		}
	}

	// --- 0: plain-string user prompt ---
	u0, ok := events[0].(sessionlog.UserMessage)
	if !ok {
		t.Fatalf("event[0] type = %T, want UserMessage", events[0])
	}
	if u0.Text != "hello, list files" {
		t.Errorf("event[0].Text = %q, want %q", u0.Text, "hello, list files")
	}
	if u0.PromptID != "prompt-1" {
		t.Errorf("event[0].PromptID = %q, want %q", u0.PromptID, "prompt-1")
	}
	if u0.UUID != "user-1" {
		t.Errorf("event[0].UUID = %q, want %q", u0.UUID, "user-1")
	}
	if got := u0.Timestamp.Format(time.RFC3339); got != "2026-05-28T10:00:00Z" {
		t.Errorf("event[0].Timestamp = %q, want %q", got, "2026-05-28T10:00:00Z")
	}

	// --- 1: assistant with thinking + text + tool_use blocks ---
	a1, ok := events[1].(sessionlog.AssistantMessage)
	if !ok {
		t.Fatalf("event[1] type = %T, want AssistantMessage", events[1])
	}
	if a1.Model != "claude-opus-4-7" {
		t.Errorf("event[1].Model = %q", a1.Model)
	}
	if a1.StopReason != "tool_use" {
		t.Errorf("event[1].StopReason = %q", a1.StopReason)
	}
	if a1.RequestID != "req-1" {
		t.Errorf("event[1].RequestID = %q", a1.RequestID)
	}
	if a1.MessageID != "msg_01" {
		t.Errorf("event[1].MessageID = %q", a1.MessageID)
	}
	if a1.Usage.InputTokens != 42 {
		t.Errorf("event[1].Usage.InputTokens = %d, want 42", a1.Usage.InputTokens)
	}
	if a1.Usage.OutputTokens != 17 {
		t.Errorf("event[1].Usage.OutputTokens = %d, want 17", a1.Usage.OutputTokens)
	}
	if a1.Usage.CacheCreation.Ephemeral5mInputTokens != 800 {
		t.Errorf("event[1].Usage.CacheCreation.5m = %d, want 800",
			a1.Usage.CacheCreation.Ephemeral5mInputTokens)
	}
	if a1.Usage.CacheCreation.Ephemeral1hInputTokens != 200 {
		t.Errorf("event[1].Usage.CacheCreation.1h = %d, want 200",
			a1.Usage.CacheCreation.Ephemeral1hInputTokens)
	}
	if a1.Usage.ServiceTier != "standard" {
		t.Errorf("event[1].Usage.ServiceTier = %q", a1.Usage.ServiceTier)
	}
	if got, want := len(a1.ContentBlocks), 3; got != want {
		t.Fatalf("event[1] content blocks = %d, want %d", got, want)
	}
	thinking, ok := a1.ContentBlocks[0].(sessionlog.ThinkingBlock)
	if !ok {
		t.Fatalf("block[0] type = %T, want ThinkingBlock", a1.ContentBlocks[0])
	}
	if thinking.Thinking != "need to call ls" {
		t.Errorf("thinking.Thinking = %q", thinking.Thinking)
	}
	if thinking.Signature != "sig-abc" {
		t.Errorf("thinking.Signature = %q", thinking.Signature)
	}
	text, ok := a1.ContentBlocks[1].(sessionlog.TextBlock)
	if !ok {
		t.Fatalf("block[1] type = %T, want TextBlock", a1.ContentBlocks[1])
	}
	if text.Text != "I'll list the files." {
		t.Errorf("text.Text = %q", text.Text)
	}
	tu, ok := a1.ContentBlocks[2].(sessionlog.ToolUseBlock)
	if !ok {
		t.Fatalf("block[2] type = %T, want ToolUseBlock", a1.ContentBlocks[2])
	}
	if tu.Name != "Bash" {
		t.Errorf("toolUse.Name = %q", tu.Name)
	}
	if tu.ID != "toolu_01" {
		t.Errorf("toolUse.ID = %q", tu.ID)
	}
	// Input should be a JSON object that round-trips into {"command":"ls"}.
	var inputObj map[string]string
	if err := json.Unmarshal(tu.Input, &inputObj); err != nil {
		t.Fatalf("toolUse.Input decode: %v", err)
	}
	if inputObj["command"] != "ls" {
		t.Errorf("toolUse.Input[command] = %q, want %q", inputObj["command"], "ls")
	}

	// --- 2: tool_result with string content ---
	u2, ok := events[2].(sessionlog.UserMessage)
	if !ok {
		t.Fatalf("event[2] type = %T, want UserMessage", events[2])
	}
	if u2.Text != "" {
		t.Errorf("event[2].Text = %q, want empty (array content)", u2.Text)
	}
	if got, want := len(u2.ContentBlocks), 1; got != want {
		t.Fatalf("event[2] blocks = %d, want %d", got, want)
	}
	tr2, ok := u2.ContentBlocks[0].(sessionlog.ToolResultBlock)
	if !ok {
		t.Fatalf("event[2] block[0] type = %T, want ToolResultBlock", u2.ContentBlocks[0])
	}
	if tr2.ToolUseID != "toolu_01" {
		t.Errorf("event[2] toolResult.ToolUseID = %q", tr2.ToolUseID)
	}
	if tr2.IsError {
		t.Errorf("event[2] toolResult.IsError = true, want false")
	}
	if tr2.Text != "file1.txt\nfile2.txt" {
		t.Errorf("event[2] toolResult.Text = %q", tr2.Text)
	}
	if tr2.Blocks != nil {
		t.Errorf("event[2] toolResult.Blocks should be nil for string content")
	}

	// --- 3: tool_result with array content (sub-blocks) ---
	u3, ok := events[3].(sessionlog.UserMessage)
	if !ok {
		t.Fatalf("event[3] type = %T, want UserMessage", events[3])
	}
	tr3, ok := u3.ContentBlocks[0].(sessionlog.ToolResultBlock)
	if !ok {
		t.Fatalf("event[3] block[0] type = %T", u3.ContentBlocks[0])
	}
	if !tr3.IsError {
		t.Errorf("event[3] toolResult.IsError = false, want true")
	}
	if tr3.Text != "" {
		t.Errorf("event[3] toolResult.Text = %q, want empty (array content)", tr3.Text)
	}
	if got, want := len(tr3.Blocks), 1; got != want {
		t.Fatalf("event[3] toolResult.Blocks = %d, want %d", got, want)
	}
	subText, ok := tr3.Blocks[0].(sessionlog.TextBlock)
	if !ok {
		t.Fatalf("event[3] toolResult.Blocks[0] type = %T", tr3.Blocks[0])
	}
	if subText.Text != "permission denied" {
		t.Errorf("event[3] subText.Text = %q", subText.Text)
	}

	// --- 4: sidechain assistant (subagent record) ---
	a4, ok := events[4].(sessionlog.AssistantMessage)
	if !ok {
		t.Fatalf("event[4] type = %T", events[4])
	}
	if !a4.IsSidechain {
		t.Errorf("event[4].IsSidechain = false, want true")
	}
	if a4.ParentUUID != "user-3" {
		t.Errorf("event[4].ParentUUID = %q", a4.ParentUUID)
	}

	// --- 5: system record ---
	sys, ok := events[5].(sessionlog.SystemMessage)
	if !ok {
		t.Fatalf("event[5] type = %T, want SystemMessage", events[5])
	}
	if sys.Subtype != "stop_hook_summary" {
		t.Errorf("system.Subtype = %q", sys.Subtype)
	}
	if sys.Level != "suggestion" {
		t.Errorf("system.Level = %q", sys.Level)
	}

	// --- 6-10: metadata records fall through to RawEvent ---
	for _, idx := range []int{6, 7, 8, 9, 10} {
		if _, ok := events[idx].(sessionlog.RawEvent); !ok {
			t.Errorf("event[%d] type = %T, want RawEvent (metadata fall-through)", idx, events[idx])
		}
	}
	mode := events[6].(sessionlog.RawEvent)
	if mode.Kind() != "mode" {
		t.Errorf("event[6].Kind() = %q, want %q", mode.Kind(), "mode")
	}
	if mode.ParseError != nil {
		t.Errorf("event[6] should not be a parse error: %v", mode.ParseError)
	}

	// --- 11: malformed JSON → RawEvent with ParseError set ---
	broken, ok := events[11].(sessionlog.RawEvent)
	if !ok {
		t.Fatalf("event[11] type = %T, want RawEvent", events[11])
	}
	if broken.ParseError == nil {
		t.Errorf("event[11].ParseError = nil, want non-nil")
	}
	if len(broken.RawLine()) == 0 {
		t.Errorf("event[11].RawLine() empty, want broken bytes")
	}

	// --- 12: assistant with an unknown block type (server_tool_call) ---
	a12, ok := events[12].(sessionlog.AssistantMessage)
	if !ok {
		t.Fatalf("event[12] type = %T", events[12])
	}
	if got, want := len(a12.ContentBlocks), 2; got != want {
		t.Fatalf("event[12] blocks = %d, want %d", got, want)
	}
	unknownBlock, ok := a12.ContentBlocks[1].(sessionlog.RawBlock)
	if !ok {
		t.Fatalf("event[12].ContentBlocks[1] type = %T, want RawBlock", a12.ContentBlocks[1])
	}
	if unknownBlock.TypeName != "server_tool_call" {
		t.Errorf("unknownBlock.TypeName = %q", unknownBlock.TypeName)
	}
	if !bytes.Contains(unknownBlock.Raw, []byte(`"web_fetch"`)) {
		t.Errorf("unknownBlock.Raw missing expected payload: %s", string(unknownBlock.Raw))
	}
}

// TestParseLine_EmptyLine ensures Stream tolerates whitespace-only
// lines without producing a ParseError. The fixture intentionally
// doesn't include blank lines, but concatenated transcripts may.
func TestParseLine_EmptyLine(t *testing.T) {
	t.Parallel()
	for _, line := range [][]byte{nil, {}, []byte("   "), []byte("\t\t")} {
		ev := sessionlog.ParseLine(line)
		raw, ok := ev.(sessionlog.RawEvent)
		if !ok {
			t.Errorf("ParseLine(%q) → %T, want RawEvent", line, ev)
			continue
		}
		if raw.ParseError != nil {
			t.Errorf("ParseLine(%q).ParseError = %v, want nil", line, raw.ParseError)
		}
	}
}

// TestStream_DoesNotShareBuffer guards against the scanner reuse bug:
// bufio.Scanner reuses its internal buffer, so an Event that stashes
// the bytes by reference would see them clobbered on the next Scan().
// Stream copies before handing the slice off; this test exercises
// that contract by comparing the RawLine of every event against a
// snapshot taken before fully draining the channel.
func TestStream_DoesNotShareBuffer(t *testing.T) {
	t.Parallel()
	input := strings.NewReader(strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"first"}}`,
		`{"type":"user","message":{"role":"user","content":"second"}}`,
		`{"type":"user","message":{"role":"user","content":"third"}}`,
		"",
	}, "\n"))
	var collected []sessionlog.Event
	for ev := range sessionlog.Stream(input) {
		collected = append(collected, ev)
	}
	if len(collected) != 3 {
		t.Fatalf("got %d events, want 3", len(collected))
	}
	wants := []string{"first", "second", "third"}
	for i, ev := range collected {
		um := ev.(sessionlog.UserMessage)
		if um.Text != wants[i] {
			t.Errorf("event[%d].Text = %q, want %q (buffer clobbered?)", i, um.Text, wants[i])
		}
		// Independently verify the stored RawLine still contains the
		// matching content marker (the bug would manifest as every
		// raw line equalling the last-read one).
		if !bytes.Contains(ev.RawLine(), []byte(wants[i])) {
			t.Errorf("event[%d].RawLine() missing %q: %s", i, wants[i], string(ev.RawLine()))
		}
	}
}

// TestStream_TailingReaderBlocks ensures Stream's goroutine doesn't
// busy-loop on a blocking reader. We wrap a pipe whose writer never
// closes mid-test; Stream should sit on the channel after consuming
// the available bytes, then drain + close cleanly when we shut the
// writer. This mirrors how nxadm/tail's Reader will behave in the
// CLI verb's --follow path.
func TestStream_TailingReaderBlocks(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	ch := sessionlog.Stream(pr)

	go func() {
		_, _ = io.WriteString(pw, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n")
		// Hold the pipe open briefly so the scanner cannot EOF-finish
		// just because we haven't written anything else yet.
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(pw, `{"type":"user","message":{"role":"user","content":"bye"}}`+"\n")
		_ = pw.Close()
	}()

	var got []string
	for ev := range ch {
		um, ok := ev.(sessionlog.UserMessage)
		if !ok {
			t.Errorf("unexpected event type %T", ev)
			continue
		}
		got = append(got, um.Text)
	}
	if len(got) != 2 || got[0] != "hi" || got[1] != "bye" {
		t.Errorf("got %v, want [hi bye]", got)
	}
}
