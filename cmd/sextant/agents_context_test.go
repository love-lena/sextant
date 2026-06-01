package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/sessionlog"
)

// renderFixture opens a staged JSONL fixture and renders it through the
// shared renderEvents core under the given mode — the side-effect-free
// path both the --raw/--backup dump and the -i viewport feed. (The live
// `agents context` default reads frames, not files; the modes' fidelity
// is asserted here off the authoritative .jsonl source.)
func renderFixture(t *testing.T, path string, mode sessionlog.Mode) (string, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	rerr := renderEvents(&buf, sessionlog.Stream(f), mode)
	return buf.String(), rerr
}

// fixtureLines is the JSONL the tests below stream through the
// renderer. Kept small + curated so each assertion picks out one
// specific behaviour:
//
//   - line 0: assistant turn with thinking, text, tool_use, usage
//   - line 1: user tool_result string
//   - line 2: subagent assistant (isSidechain=true)
//   - line 3: metadata RawEvent (mode) — appears in raw only
const fixtureLines = `{"type":"assistant","parentUuid":"u0","isSidechain":false,"requestId":"r1","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","type":"message","content":[{"type":"thinking","thinking":"plan ahead"},{"type":"text","text":"hi there"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":80,"ephemeral_1h_input_tokens":20}}},"uuid":"a1","sessionId":"s1"}
{"type":"user","parentUuid":"a1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"hello world"}]},"uuid":"u1","sessionId":"s1"}
{"type":"assistant","parentUuid":"u1","isSidechain":true,"message":{"id":"m2","model":"claude-opus-4-7","role":"assistant","type":"message","content":[{"type":"text","text":"sub"}],"stop_reason":"end_turn"},"uuid":"a2","sessionId":"s1"}
{"type":"mode","mode":"normal","sessionId":"s1"}
`

// writeFixture stages fixtureLines into a fresh tempfile. Tests then
// point runAgentsContext at it with each --mode value.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(path, []byte(fixtureLines), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestParseContextModeAcceptsAllCanonical — every mode listed in the
// closed enum must round-trip. Empty maps to the raw default.
func TestParseContextModeAcceptsAllCanonical(t *testing.T) {
	t.Parallel()
	cases := []string{"raw", "conversation", "tools", "thinking", "usage", "tree", "", "RAW", " usage  "}
	for _, in := range cases {
		if _, err := sessionlog.ParseMode(in); err != nil {
			t.Errorf("sessionlog.ParseMode(%q) err=%v, want nil", in, err)
		}
	}
}

// TestParseContextModeRejectsUnknown — bogus values must surface a
// usage error mentioning the legal set.
func TestParseContextModeRejectsUnknown(t *testing.T) {
	t.Parallel()
	_, err := sessionlog.ParseMode("garbage")
	if err == nil {
		t.Fatal("expected error for bogus mode")
	}
	for _, want := range []string{"raw", "conversation", "tree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing legal mode %q: %v", want, err)
		}
	}
}

// TestRunAgentsContextRawMode — raw mode prints every line verbatim
// (including the metadata `mode` record that the typed modes drop).
func TestRunAgentsContextRawMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	got, err := renderFixture(t, path, sessionlog.ModeRaw)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	wantSubstrings := []string{
		`"type":"assistant"`,
		`"type":"user"`,
		`"type":"mode"`,
		`"isSidechain":true`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("raw mode missing %q in output:\n%s", want, got)
		}
	}
	// Exactly four lines (including a trailing newline from the last
	// fmt.Fprintf), so split on "\n" and drop the empty tail.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Errorf("raw mode line count = %d, want 4", len(lines))
	}
}

// TestRunAgentsContextConversationFiltersMetadata — conversation mode
// surfaces assistant/user/tool prose only; the metadata `mode` record
// is dropped.
func TestRunAgentsContextConversationMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	got, err := renderFixture(t, path, sessionlog.ModeConversation)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	wantSubstrings := []string{
		"assistant: hi there",
		"tool_use[t1] Bash",
		"tool_result[ok] t1: hello world",
		"assistant: sub",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("conversation mode missing %q in:\n%s", want, got)
		}
	}
	// `mode` metadata must NOT leak into conversation mode.
	if strings.Contains(got, "\"type\":\"mode\"") {
		t.Errorf("conversation mode leaked metadata: %s", got)
	}
}

// TestRunAgentsContextToolsMode — only tool_use + tool_result are
// printed; thinking / text / system records are silent.
func TestRunAgentsContextToolsMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	got, err := renderFixture(t, path, sessionlog.ModeTools)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	if !strings.Contains(got, "call t1 Bash") {
		t.Errorf("tools mode missing call: %s", got)
	}
	if !strings.Contains(got, "result[ok] t1: hello world") {
		t.Errorf("tools mode missing result: %s", got)
	}
	if strings.Contains(got, "hi there") || strings.Contains(got, "sub") {
		t.Errorf("tools mode leaked text content: %s", got)
	}
}

// TestRunAgentsContextThinkingMode — only thinking blocks should
// emit; their parent UUID anchors the operator to the turn.
func TestRunAgentsContextThinkingMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	got, err := renderFixture(t, path, sessionlog.ModeThinking)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	if !strings.Contains(got, "thinking[a1]: plan ahead") {
		t.Errorf("thinking mode missing record: %s", got)
	}
	if strings.Contains(got, "tool_use") || strings.Contains(got, "tool_result") {
		t.Errorf("thinking mode leaked non-thinking output: %s", got)
	}
}

// TestRunAgentsContextUsageMode — accumulates per-turn usage stats
// and prints the rollup. Only the first fixture turn carries usage;
// the second assistant record has zero usage and must be skipped.
func TestRunAgentsContextUsageMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	raw, err := renderFixture(t, path, sessionlog.ModeUsage)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	got := strings.TrimSpace(raw)
	// Exactly one usage line (the second assistant turn carries
	// zero-value usage and is skipped by the tracker).
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("usage mode lines = %d, want 1:\n%s", len(lines), got)
	}
	wantSubstrings := []string{
		"turn=1",
		"in=10",
		"out=5",
		"cache_create=100",
		"5m=80",
		"1h=20",
		"cache_read=50",
		"model=claude-opus-4-7",
		"stop=tool_use",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("usage mode missing %q in %q", want, got)
		}
	}
}

// TestRunAgentsContextTreeMode — flat tree projection labels
// sidechain records and carries parentUuid + kind, so the operator
// can grep for subagent dispatches even pre-TUI.
func TestRunAgentsContextTreeMode(t *testing.T) {
	t.Parallel()
	path := writeFixture(t)
	got, err := renderFixture(t, path, sessionlog.ModeTree)
	if err != nil {
		t.Fatalf("renderFixture: %v", err)
	}
	if !strings.Contains(got, "[main] a1 parent=u0 kind=assistant") {
		t.Errorf("tree mode missing main row: %s", got)
	}
	if !strings.Contains(got, "[sidechain] a2 parent=u1 kind=assistant") {
		t.Errorf("tree mode missing sidechain row: %s", got)
	}
}

// TestRunAgentsContextOpenError — a nonexistent authoritative-source
// path surfaces a readable error rather than panicking. Mirrors the
// dump path's os.Open of the in-container read result / host snapshot.
func TestRunAgentsContextOpenError(t *testing.T) {
	t.Parallel()
	_, err := renderFixture(t, filepath.Join(t.TempDir(), "missing.jsonl"), sessionlog.ModeRaw)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("error = %v, want a not-exist error", err)
	}
}

// TestOneLine_FoldsAndTruncates verifies the renderer's per-line
// width cap — without it a 200KiB Read tool_result blows out the
// terminal on raw=tools mode.
func TestOneLine_FoldsAndTruncates(t *testing.T) {
	t.Parallel()
	got := sessionlog.OneLine("hello\nworld\nfoo")
	if got != "hello world foo" {
		t.Errorf("got %q, want %q", got, "hello world foo")
	}
	long := strings.Repeat("x", 1000)
	out := sessionlog.OneLine(long)
	if len(out) > 250 {
		t.Errorf("oneLine did not truncate: len=%d", len(out))
	}
	if !strings.HasSuffix(out, "…") {
		t.Errorf("oneLine missing ellipsis: %q", out)
	}
}
