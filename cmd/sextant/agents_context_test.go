package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/sessionlog"
)

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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeRaw, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := buf.String()
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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeConversation, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := buf.String()
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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeTools, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := buf.String()
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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeThinking, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := buf.String()
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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeUsage, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := strings.TrimSpace(buf.String())
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
	var buf bytes.Buffer
	if err := runAgentsContext(context.Background(), &buf, path, sessionlog.ModeTree, false); err != nil {
		t.Fatalf("runAgentsContext: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "[main] a1 parent=u0 kind=assistant") {
		t.Errorf("tree mode missing main row: %s", got)
	}
	if !strings.Contains(got, "[sidechain] a2 parent=u1 kind=assistant") {
		t.Errorf("tree mode missing sidechain row: %s", got)
	}
}

// TestRunAgentsContextOpenError — a nonexistent path surfaces a
// readable error rather than panicking.
func TestRunAgentsContextOpenError(t *testing.T) {
	t.Parallel()
	err := runAgentsContext(context.Background(), &bytes.Buffer{},
		filepath.Join(t.TempDir(), "missing.jsonl"), sessionlog.ModeRaw, false)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "open session log") {
		t.Errorf("error = %v, want substring 'open session log'", err)
	}
}

// TestResolveSessionJSONLPath_DirectFile — covers the SDK layout
// where the JSONL lives directly under projectsDir (no per-cwd
// subdir). This is the common shape inside the sidecar's bind-mounted
// projects dir because each agent has one cwd → one subdir.
func TestResolveSessionJSONLPath_DirectFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "abc-123.jsonl"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := resolveSessionJSONLPath(dir, "abc-123")
	if err != nil {
		t.Fatalf("resolveSessionJSONLPath: %v", err)
	}
	if got != filepath.Join(dir, "abc-123.jsonl") {
		t.Errorf("got %q", got)
	}
}

// TestResolveSessionJSONLPath_PerCwdSubdir — covers the SDK's
// canonical layout where each cwd gets a URL-encoded subdir.
func TestResolveSessionJSONLPath_PerCwdSubdir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "-workspace")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := filepath.Join(sub, "abc-123.jsonl")
	if err := os.WriteFile(want, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := resolveSessionJSONLPath(dir, "abc-123")
	if err != nil {
		t.Fatalf("resolveSessionJSONLPath: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveSessionJSONLPath_DirMissing — when the per-agent projects
// dir doesn't exist at all (agent spawned before the context bind-mount,
// or no turn flushed), the operator must get a friendly, actionable
// message — NOT a raw os.ReadDir ENOENT leaked as INTERNAL. This is the
// `sextant agents context assistant` bug: a non-nil SessionLog in KV
// pointing at a dir that was never created on disk.
//
// Pattern (per plans/issues/feat-tui-launch-acceptance-gate.md): assert
// error *messages* on operator surfaces are friendly + actionable and
// don't leak filesystem internals.
func TestResolveSessionJSONLPath_DirMissing(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "never-created", "claude-projects")
	_, err := resolveSessionJSONLPath(missing, "abc-123")
	if err == nil {
		t.Fatal("expected error for a non-existent projects dir")
	}
	// Must NOT leak the raw os.ReadDir error.
	if strings.Contains(err.Error(), "read projects dir") ||
		strings.Contains(err.Error(), "no such file or directory") {
		t.Errorf("leaked raw filesystem error to the operator: %v", err)
	}
	// Must be actionable.
	if !strings.Contains(err.Error(), "retry") {
		t.Errorf("error is not actionable (no next step): %v", err)
	}
}

// TestResolveSessionJSONLPath_NotFound — surfaces a readable error
// when no matching JSONL exists. Common during the warm-up window
// before the sidecar has flushed its first turn.
func TestResolveSessionJSONLPath_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := resolveSessionJSONLPath(dir, "abc-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want substring 'not found'", err)
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
