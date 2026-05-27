# Chat TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/tui/chat/`, a modal (NORMAL/INSERT) Bubble Tea chat window for `sextant conversation <agent>`, and wire it into `cmd/sextant/conversation.go` so the TUI is the default surface while `--json` keeps the NDJSON streamer and `--read` opens a composer-less read-only variant.

**Architecture:** A library package `pkg/tui/chat/` that exposes a `Run(...)` entry point taking the same NATS subscription channels and `prompt_agent` RPC dispatcher that `cmd/sextant/conversation.go` already builds. The package is a standard bubbletea triple — `Model` / `Update` / `View` — with `frames.go` translating NATS messages into typed `tea.Msg`s and `turn.go` collapsing the frame stream into the operator-facing `Turn` model (assistant turns own their tool-call frames; tool calls don't get their own row). Role-token styles in `style.go` mean every accent (selection mark, active border, attention, destructive) is named once and reused — no hard-coded hex in layout code. The existing NDJSON output path stays byte-identical; the TUI is an additive default that activates when stdout is a TTY and `--json` is not set.

**Tech Stack:** Go 1.26, `github.com/charmbracelet/bubbletea` (already in go.mod at v1.3.10), `github.com/charmbracelet/lipgloss` (v1.1.0), `github.com/charmbracelet/bubbles` (NEW — to be added). Internal: `pkg/client` (NATS pub/sub + RPC), `pkg/sextantproto` (envelope + payload types), `pkg/rpc` (verb constants).

**Spec reference:** `plans/issues/feat-chat-tui.md`. Acceptance tests are listed in the spec — this plan covers each one in the task it belongs to (see "Spec coverage" at the bottom).

---

## File Structure

New package — every file lives in `pkg/tui/chat/`:

- `style.go` — central lipgloss role tokens (one place for every accent color). Single responsibility: name-to-style lookup.
- `turn.go` — pure data: `Turn`, `ToolCall`, `Actor` types; `FramesToTurns([]Frame) []Turn` collapses raw frames into the operator-visible turn list (tool calls become children of their parent assistant turn). No bubbletea imports.
- `keys.go` — `key.Binding` sets keyed by mode. Used by both the reducer (for matching) and the status bar (for the displayed hint chips). Single responsibility: key vocabulary.
- `model.go` — `Model` struct + constructor `New(opts Options) Model` + `Init() tea.Cmd` + `Update(tea.Msg) (tea.Model, tea.Cmd)`. The reducer is the only consumer of `keys.go` bindings.
- `view.go` — `View()` composition: header → stream (viewport) → composer (textarea, hidden in read mode) → status bar. Layout-only — accents come from `style.go`, never inline.
- `frames.go` — converts `<-chan client.Message` for frames+lifecycle into typed `tea.Msg`s (`frameMsg`, `lifecycleMsg`, `subscriptionEndedMsg`) via a `tea.Cmd` that's re-issued after each receive (the "wait-for-next-message" Bubble Tea pattern).
- `program.go` — `Run(ctx, opts)` constructs a `tea.Program`, starts it, and returns; the only file `cmd/sextant/conversation.go` needs to import. Also exposes the send hook (`SendDraft`) so the model can dispatch `prompt_agent` RPCs without importing `pkg/client` directly — the bus interface lives here.

Tests live alongside (`style_test.go`, `turn_test.go`, `model_test.go`, `view_test.go`, `program_test.go`).

CLI wiring touches one existing file:
- `cmd/sextant/conversation.go` — modify `runConversation` to launch `chat.Run(...)` by default, keep `--json` on the existing NDJSON path, propagate `--read` and `--tail`.

---

## Task 1: Bootstrap package + add bubbles dependency

**Files:**
- Create: `pkg/tui/chat/doc.go`
- Create: `pkg/tui/chat/style.go`
- Create: `pkg/tui/chat/style_test.go`
- Modify: `go.mod` (add `github.com/charmbracelet/bubbles`)
- Modify: `go.sum` (`go mod tidy`)

- [ ] **Step 1: Add the bubbles dependency**

Run: `go get github.com/charmbracelet/bubbles@latest && go mod tidy`
Expected: `go.mod` gains `github.com/charmbracelet/bubbles vX.Y.Z`, `go.sum` updated.

- [ ] **Step 2: Create the package doc file**

```go
// Package chat is the modal (NORMAL/INSERT) Bubble Tea chat window for
// `sextant conversation <agent>`. The package shape and conventions are
// the precedent for future per-surface TUIs (audit, pending, …) co-located
// under `pkg/tui/`.
//
// Spec: plans/issues/feat-chat-tui.md
// Plan: plans/feat-chat-tui-impl.md
package chat
```

Write to `pkg/tui/chat/doc.go`.

- [ ] **Step 3: Write the failing role-token test**

```go
package chat

import (
	"testing"
)

// TestStyleRoleTokensDefined asserts every named role this package
// uses is reachable from defaultStyles() and renders to a non-empty
// ANSI string. The point is not visual fidelity — it's that callers
// never reach past the role lookup into raw hex.
func TestStyleRoleTokensDefined(t *testing.T) {
	t.Parallel()
	s := defaultStyles()
	roles := map[string]func() string{
		"activeBorder":   func() string { return s.ActiveBorder.Render("x") },
		"selectMark":     func() string { return s.SelectMark.Render("x") },
		"attention":      func() string { return s.Attention.Render("x") },
		"destructive":    func() string { return s.Destructive.Render("x") },
		"success":        func() string { return s.Success.Render("x") },
		"muted":          func() string { return s.Muted.Render("x") },
		"headerName":     func() string { return s.HeaderName.Render("x") },
		"headerBranch":   func() string { return s.HeaderBranch.Render("x") },
		"actorUser":      func() string { return s.ActorUser.Render("x") },
		"actorAgent":     func() string { return s.ActorAgent.Render("x") },
		"toolLine":       func() string { return s.ToolLine.Render("x") },
		"statusNormal":   func() string { return s.StatusNormal.Render("x") },
		"statusInsert":   func() string { return s.StatusInsert.Render("x") },
		"statusRead":     func() string { return s.StatusRead.Render("x") },
		"composerActive": func() string { return s.ComposerActive.Render("x") },
		"composerParked": func() string { return s.ComposerParked.Render("x") },
	}
	for name, render := range roles {
		if got := render(); got == "" {
			t.Errorf("role %s: rendered empty string", name)
		}
	}
}
```

Write to `pkg/tui/chat/style_test.go`.

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/...`
Expected: FAIL with "defaultStyles undefined" or compile error.

- [ ] **Step 5: Implement style.go**

```go
package chat

import "github.com/charmbracelet/lipgloss"

// Styles is the package's role-token table. Every accent used anywhere
// in the chat TUI is named here and looked up by role — never inlined.
// This is what lets the visual treatment evolve without touching layout
// code in view.go. Spec §"Design system".
type Styles struct {
	// Surface-level accents.
	ActiveBorder lipgloss.Style // focused surface border (composer in INSERT)
	SelectMark   lipgloss.Style // selected turn left-border + tint
	Attention    lipgloss.Style // needs operator ack (e.g. pending permission badge)
	Destructive  lipgloss.Style // failed tool call, dangerous action
	Success      lipgloss.Style // ok tool call

	Muted        lipgloss.Style // de-emphasized text (timestamps, branch, hints)
	HeaderName   lipgloss.Style // agent name in header
	HeaderBranch lipgloss.Style // branch ref next to name

	ActorUser  lipgloss.Style // user turn glyph + name
	ActorAgent lipgloss.Style // agent turn glyph + name
	ToolLine   lipgloss.Style // tool-call line under a turn

	StatusNormal lipgloss.Style // NORMAL mode pill (outlined)
	StatusInsert lipgloss.Style // INSERT mode pill (filled)
	StatusRead   lipgloss.Style // READ pill in --read mode

	ComposerActive lipgloss.Style // composer when INSERT is active
	ComposerParked lipgloss.Style // composer when NORMAL is active (dimmed)
}

// defaultStyles returns the baseline role-token table. ANSI numeric
// colors keep the rendering portable across terminal palettes.
func defaultStyles() Styles {
	const (
		colAccent      = lipgloss.Color("4")  // blue
		colSelect      = lipgloss.Color("6")  // cyan
		colAttention   = lipgloss.Color("3")  // yellow
		colDestructive = lipgloss.Color("1")  // red
		colSuccess     = lipgloss.Color("2")  // green
		colMuted       = lipgloss.Color("8")  // bright black
		colText        = lipgloss.Color("15") // bright white
	)
	bold := lipgloss.NewStyle().Bold(true)
	return Styles{
		ActiveBorder:   lipgloss.NewStyle().Foreground(colAccent),
		SelectMark:     lipgloss.NewStyle().Foreground(colSelect).Bold(true),
		Attention:      lipgloss.NewStyle().Foreground(colAttention).Bold(true),
		Destructive:    lipgloss.NewStyle().Foreground(colDestructive),
		Success:        lipgloss.NewStyle().Foreground(colSuccess),
		Muted:          lipgloss.NewStyle().Foreground(colMuted),
		HeaderName:     bold.Foreground(colText),
		HeaderBranch:   lipgloss.NewStyle().Foreground(colMuted),
		ActorUser:      bold.Foreground(colSelect),
		ActorAgent:     bold.Foreground(colAccent),
		ToolLine:       lipgloss.NewStyle().Foreground(colMuted),
		StatusNormal:   lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		StatusInsert:   lipgloss.NewStyle().Foreground(colText).Background(colAccent).Bold(true).Padding(0, 1),
		StatusRead:     lipgloss.NewStyle().Foreground(colMuted).Bold(true),
		ComposerActive: lipgloss.NewStyle().Foreground(colText),
		ComposerParked: lipgloss.NewStyle().Foreground(colMuted),
	}
}
```

Write to `pkg/tui/chat/style.go`.

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./pkg/tui/chat/... -run TestStyleRoleTokensDefined -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum pkg/tui/chat/doc.go pkg/tui/chat/style.go pkg/tui/chat/style_test.go
git commit -m "chat-tui: bootstrap pkg/tui/chat with style role tokens"
```

---

## Task 2: Turn model + frame→turn collapse

**Files:**
- Create: `pkg/tui/chat/turn.go`
- Create: `pkg/tui/chat/turn_test.go`

- [ ] **Step 1: Write failing tests for turn collapsing**

```go
package chat

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func envAt(t time.Time, kind sextantproto.FrameKind, body map[string]any, toolName string) Frame {
	return Frame{
		Ts:        t,
		FrameKind: kind,
		ToolName:  toolName,
		Body:      body,
	}
}

func TestFramesToTurns_GroupsToolCallsUnderPriorAssistantTurn(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "calling read_file"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"path": "main.go"}, "read_file"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(120)}, "read_file"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "done"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Actor != ActorAgent || turns[0].Text != "calling read_file" {
		t.Errorf("turn0: %+v", turns[0])
	}
	if len(turns[0].ToolCalls) != 1 {
		t.Fatalf("turn0 tool calls: want 1, got %d", len(turns[0].ToolCalls))
	}
	tc := turns[0].ToolCalls[0]
	if tc.Name != "read_file" || tc.Status != ToolStatusOK {
		t.Errorf("tool call: %+v", tc)
	}
	if turns[1].Actor != ActorAgent || turns[1].Text != "done" {
		t.Errorf("turn1: %+v", turns[1])
	}
}

func TestFramesToTurns_UserPromptStartsNewTurn(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		{Ts: t0, Actor: ActorUser, Text: "hi"},
		envAt(t0.Add(time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "hello"}, ""),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Actor != ActorUser || turns[0].Text != "hi" {
		t.Errorf("turn0: %+v", turns[0])
	}
	if turns[1].Actor != ActorAgent {
		t.Errorf("turn1 actor: %v", turns[1].Actor)
	}
}

func TestFramesToTurns_ToolCallFailureMarksStatusFailed(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "trying"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{}, "bad_tool"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"error": "boom"}, "bad_tool"),
	}
	turns := FramesToTurns(frames)
	if len(turns) != 1 || len(turns[0].ToolCalls) != 1 {
		t.Fatalf("turns=%+v", turns)
	}
	if got := turns[0].ToolCalls[0].Status; got != ToolStatusFailed {
		t.Errorf("status: want failed, got %v", got)
	}
}
```

Write to `pkg/tui/chat/turn_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/... -run TestFramesToTurns -v`
Expected: FAIL (Frame/Turn/FramesToTurns undefined).

- [ ] **Step 3: Implement turn.go**

```go
package chat

import (
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Actor identifies who emitted a turn. Used to pick the row glyph and
// the actor accent style.
type Actor int

const (
	ActorUnknown Actor = iota
	ActorUser
	ActorAgent
	ActorSystem
)

// ToolStatus is the outcome of a tool call, derived from the matching
// tool_result frame's body. Used by view.go to pick success vs
// destructive role tokens — never by direct color lookup.
type ToolStatus int

const (
	ToolStatusPending ToolStatus = iota
	ToolStatusOK
	ToolStatusFailed
)

// ToolCall is one tool invocation attached to its parent assistant
// turn. The Duration field is the time between the tool_call and the
// matching tool_result; it's zero until the result arrives.
type ToolCall struct {
	Name     string
	Arg      string // short summary of the most salient body field
	Status   ToolStatus
	Duration time.Duration
	StartTs  time.Time
}

// Turn is one row in the chat stream. Tool calls render as indented
// lines under the assistant turn that emitted them — not as separate
// rows. Spec §"Stream rendering".
type Turn struct {
	Ts        time.Time
	Actor     Actor
	Text      string
	ToolCalls []ToolCall
}

// Frame is the package's intake type: one observation from the
// agent. It carries either a decoded AgentFramePayload's salient
// fields OR a synthetic local-echo entry (used for the operator's
// just-sent prompt before the real frame round-trips). The Actor
// field, when non-zero, wins over FrameKind for actor classification.
type Frame struct {
	Ts        time.Time
	FrameKind sextantproto.FrameKind
	ToolName  string
	Body      map[string]any
	// Actor optionally overrides the FrameKind→actor mapping. Used for
	// locally-echoed user prompts (they have no FrameKind because the
	// operator generated them locally, not the agent SDK).
	Actor Actor
	// Text optionally overrides the body-derived text. Set for
	// locally-echoed user prompts.
	Text string
}

// FramesToTurns collapses a frame sequence into the operator-visible
// turn list. Rules:
//   - User frames (Actor=ActorUser) start a new turn.
//   - Assistant text frames start a new agent turn.
//   - Tool call / tool result frames attach to the most-recent agent
//     turn. If no agent turn exists yet, a synthetic one is created so
//     they have somewhere to live.
//   - System notes and errors land as their own turns.
func FramesToTurns(frames []Frame) []Turn {
	var turns []Turn
	// tcIndex tracks the index in turns[].ToolCalls of an open tool
	// call (by tool name) so the matching tool_result can patch its
	// Status + Duration in place.
	type openCall struct {
		turn    int
		callIdx int
	}
	open := map[string]openCall{}
	for _, f := range frames {
		actor := f.Actor
		if actor == ActorUnknown {
			switch f.FrameKind {
			case sextantproto.FrameAssistantText, sextantproto.FrameToolCall, sextantproto.FrameToolResult:
				actor = ActorAgent
			case sextantproto.FrameSystemNote, sextantproto.FrameError:
				actor = ActorSystem
			}
		}
		switch {
		case actor == ActorUser:
			text := f.Text
			if text == "" {
				text, _ = f.Body["text"].(string)
			}
			turns = append(turns, Turn{Ts: f.Ts, Actor: ActorUser, Text: text})
		case f.FrameKind == sextantproto.FrameAssistantText:
			text := f.Text
			if text == "" {
				text, _ = f.Body["text"].(string)
				if text == "" {
					text, _ = f.Body["content"].(string)
				}
			}
			turns = append(turns, Turn{Ts: f.Ts, Actor: ActorAgent, Text: text})
		case f.FrameKind == sextantproto.FrameToolCall:
			ti := lastAgentTurnIndex(turns)
			if ti < 0 {
				turns = append(turns, Turn{Ts: f.Ts, Actor: ActorAgent})
				ti = len(turns) - 1
			}
			arg := summarizeArg(f.Body)
			turns[ti].ToolCalls = append(turns[ti].ToolCalls, ToolCall{
				Name:    f.ToolName,
				Arg:     arg,
				Status:  ToolStatusPending,
				StartTs: f.Ts,
			})
			open[f.ToolName] = openCall{turn: ti, callIdx: len(turns[ti].ToolCalls) - 1}
		case f.FrameKind == sextantproto.FrameToolResult:
			oc, ok := open[f.ToolName]
			if !ok {
				continue
			}
			delete(open, f.ToolName)
			call := &turns[oc.turn].ToolCalls[oc.callIdx]
			if errStr, _ := f.Body["error"].(string); errStr != "" {
				call.Status = ToolStatusFailed
			} else if statusStr, _ := f.Body["status"].(string); statusStr == "failed" {
				call.Status = ToolStatusFailed
			} else {
				call.Status = ToolStatusOK
			}
			call.Duration = f.Ts.Sub(call.StartTs)
		case f.FrameKind == sextantproto.FrameSystemNote || f.FrameKind == sextantproto.FrameError:
			text, _ := f.Body["text"].(string)
			if text == "" {
				text, _ = f.Body["message"].(string)
			}
			turns = append(turns, Turn{Ts: f.Ts, Actor: ActorSystem, Text: text})
		}
	}
	return turns
}

func lastAgentTurnIndex(turns []Turn) int {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Actor == ActorAgent {
			return i
		}
	}
	return -1
}

// summarizeArg picks one salient field from a tool-call body to render
// inline next to the tool name. Prefers "path", then "command", then
// the first string-valued field; returns "" if nothing useful.
func summarizeArg(body map[string]any) string {
	for _, k := range []string{"path", "command", "url", "query"} {
		if s, ok := body[k].(string); ok && s != "" {
			return s
		}
	}
	for _, v := range body {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
```

Write to `pkg/tui/chat/turn.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/tui/chat/... -run TestFramesToTurns -v`
Expected: PASS (all three subtests).

- [ ] **Step 5: Commit**

```bash
git add pkg/tui/chat/turn.go pkg/tui/chat/turn_test.go
git commit -m "chat-tui: Turn/ToolCall model + frame→turn collapsing"
```

---

## Task 3: Model skeleton + NORMAL-mode navigation

**Files:**
- Create: `pkg/tui/chat/keys.go`
- Create: `pkg/tui/chat/model.go`
- Create: `pkg/tui/chat/model_test.go`

- [ ] **Step 1: Write failing tests for NORMAL-mode key handling**

```go
package chat

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func seedTurns() []Turn {
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	return []Turn{
		{Ts: t0, Actor: ActorUser, Text: "hi"},
		{Ts: t0.Add(time.Second), Actor: ActorAgent, Text: "hello"},
		{Ts: t0.Add(2 * time.Second), Actor: ActorUser, Text: "next"},
		{Ts: t0.Add(3 * time.Second), Actor: ActorAgent, Text: "ok"},
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// applyKey is a test helper: feed a key to the model and return the new model.
func applyKey(t *testing.T, m Model, k tea.KeyMsg) Model {
	t.Helper()
	next, _ := m.Update(k)
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned non-Model: %T", next)
	}
	return mm
}

func TestModelInitDefaultsToNormalAtLastTurn(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"})
	m = m.WithTurns(seedTurns())
	if m.Mode() != ModeNormal {
		t.Errorf("mode: want NORMAL, got %v", m.Mode())
	}
	if got := m.Selection(); got != 3 {
		t.Errorf("selection: want 3 (last), got %d", got)
	}
}

func TestNormalJK_StepsSelection(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, key("k"))
	if got := m.Selection(); got != 2 {
		t.Errorf("after k: want 2, got %d", got)
	}
	m = applyKey(t, m, key("k"))
	m = applyKey(t, m, key("k"))
	if got := m.Selection(); got != 0 {
		t.Errorf("after 3xk: want 0, got %d", got)
	}
	m = applyKey(t, m, key("k"))
	if got := m.Selection(); got != 0 {
		t.Errorf("k clamps at 0: got %d", got)
	}
	m = applyKey(t, m, key("j"))
	if got := m.Selection(); got != 1 {
		t.Errorf("after j: want 1, got %d", got)
	}
}

func TestNormalJ_ClampsAtLastTurn(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	// selection starts at 3 (last)
	m = applyKey(t, m, key("j"))
	if got := m.Selection(); got != 3 {
		t.Errorf("j clamps at last: got %d", got)
	}
}

func TestNormalGgG_JumpsTopBottom(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, key("g"))
	m = applyKey(t, m, key("g"))
	if got := m.Selection(); got != 0 {
		t.Errorf("gg: want 0, got %d", got)
	}
	m = applyKey(t, m, key("G"))
	if got := m.Selection(); got != 3 {
		t.Errorf("G: want 3, got %d", got)
	}
}
```

Write to `pkg/tui/chat/model_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/tui/chat/... -run TestModel -v`
Expected: FAIL (New/Model/Mode/Selection/WithTurns undefined).

- [ ] **Step 3: Implement keys.go**

```go
package chat

import "github.com/charmbracelet/bubbles/key"

// keyMap is the key vocabulary for the chat TUI. Separated by mode:
// the reducer matches against the active mode's bindings, and the
// status bar renders the same bindings as hint chips. Spec §"Mode-
// aware status bar": only the keys that work in the current mode are
// shown — no busy legend of inert hotkeys.
type keyMap struct {
	// NORMAL mode
	NormalUp     key.Binding
	NormalDown   key.Binding
	NormalTop    key.Binding // 'gg' (two-key)
	NormalBottom key.Binding // 'G'
	NormalInsert key.Binding // 'i'
	NormalQuit   key.Binding // 'q' / ctrl+c

	// INSERT mode
	InsertSend    key.Binding // enter
	InsertNewline key.Binding // shift+enter
	InsertExit    key.Binding // esc
}

func defaultKeys() keyMap {
	return keyMap{
		NormalUp:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "up")),
		NormalDown:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j", "down")),
		NormalTop:     key.NewBinding(key.WithKeys("g"), key.WithHelp("gg", "top")),
		NormalBottom:  key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
		NormalInsert:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "edit")),
		NormalQuit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		InsertSend:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "send")),
		InsertNewline: key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("⇧↵", "newline")),
		InsertExit:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "back")),
	}
}
```

Write to `pkg/tui/chat/keys.go`.

- [ ] **Step 4: Implement model.go (NORMAL-mode-only skeleton — INSERT lands in Task 6)**

```go
package chat

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Mode is the modal state of the chat TUI. Spec §"MVP (Iteration 4 —
// Modal)". The default is NORMAL; INSERT is unreachable in read mode.
type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
)

// Options configure a chat Model. AgentName/Branch are header chrome;
// Read disables INSERT (and hides the composer in view.go).
type Options struct {
	AgentName string
	Branch    string
	Read      bool
}

// Model is the bubbletea reducer state. Use New to construct, then
// WithTurns to seed any pre-existing transcript before passing to
// tea.NewProgram.
type Model struct {
	opts      Options
	mode      Mode
	turns     []Turn
	selection int
	gPending  bool // first 'g' of 'gg' seen, waiting for the second
	width     int
	height    int
	styles    Styles
	keys      keyMap
	// composer + send hook land in later tasks.
}

// New returns a Model with default styles/keys, mode=NORMAL,
// selection=0 (an empty model has nothing to select).
func New(opts Options) Model {
	return Model{
		opts:   opts,
		mode:   ModeNormal,
		styles: defaultStyles(),
		keys:   defaultKeys(),
	}
}

// WithTurns seeds the transcript and parks the selection on the last
// turn (spec §"Open question 1": ship NORMAL with last turn selected).
func (m Model) WithTurns(turns []Turn) Model {
	m.turns = turns
	if len(turns) == 0 {
		m.selection = 0
	} else {
		m.selection = len(turns) - 1
	}
	return m
}

func (m Model) Mode() Mode      { return m.mode }
func (m Model) Selection() int  { return m.selection }
func (m Model) Turns() []Turn   { return m.turns }
func (m Model) IsRead() bool    { return m.opts.Read }

// Init returns no startup commands — frame subscription is wired by
// program.go and seeded via WithSubscription (Task 8).
func (m Model) Init() tea.Cmd { return nil }

// Update is the reducer. Mode-aware dispatch: in NORMAL we handle vim-
// flavored navigation; INSERT lands in Task 6.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.mode == ModeNormal {
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 'gg' is two-key; clear the pending flag on anything else.
	gPending := m.gPending
	m.gPending = false
	switch {
	case key.Matches(msg, m.keys.NormalQuit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.NormalDown):
		if m.selection < len(m.turns)-1 {
			m.selection++
		}
	case key.Matches(msg, m.keys.NormalUp):
		if m.selection > 0 {
			m.selection--
		}
	case key.Matches(msg, m.keys.NormalBottom):
		if n := len(m.turns); n > 0 {
			m.selection = n - 1
		}
	case key.Matches(msg, m.keys.NormalTop):
		if gPending {
			m.selection = 0
		} else {
			m.gPending = true
		}
	}
	return m, nil
}
```

Write to `pkg/tui/chat/model.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/tui/chat/... -run TestModel -v && go test ./pkg/tui/chat/... -run TestNormal -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/tui/chat/keys.go pkg/tui/chat/model.go pkg/tui/chat/model_test.go
git commit -m "chat-tui: NORMAL-mode navigation (j/k/gg/G) with clamping"
```

---

## Task 4: Stream rendering — header, turn rows, tool-call indent, selection accent

**Files:**
- Create: `pkg/tui/chat/view.go`
- Create: `pkg/tui/chat/view_test.go`

- [ ] **Step 1: Write the failing render test (covers spec acceptance `TestChatTUIRendersTurnsAndAttachesToolCalls`)**

```go
package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestChatTUIRendersTurnsAndAttachesToolCalls(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 34, 56, 0, time.UTC)
	frames := []Frame{
		{Ts: t0, Actor: ActorUser, Text: "read main.go"},
		envAt(t0.Add(time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "on it"}, ""),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolCall, map[string]any{"path": "main.go"}, "read_file"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolResult, map[string]any{"bytes": float64(120)}, "read_file"),
		envAt(t0.Add(4*time.Second), sextantproto.FrameAssistantText, map[string]any{"text": "120 bytes"}, ""),
	}
	m := New(Options{AgentName: "alice"}).WithTurns(FramesToTurns(frames))
	m.width, m.height = 80, 24
	out := m.View()

	// Each turn appears on its own row.
	if !strings.Contains(out, "read main.go") {
		t.Errorf("user turn missing: %q", out)
	}
	if !strings.Contains(out, "on it") {
		t.Errorf("agent turn 1 missing: %q", out)
	}
	if !strings.Contains(out, "120 bytes") {
		t.Errorf("agent turn 2 missing: %q", out)
	}
	// Tool call renders under the agent turn that emitted it — same
	// row block, not a separate turn.
	if !strings.Contains(out, "read_file") {
		t.Errorf("tool call missing: %q", out)
	}
	// Header carries the agent name.
	if !strings.Contains(out, "alice") {
		t.Errorf("header missing agent name: %q", out)
	}
	// Selected turn (last, by default) gets the SelectMark accent.
	// The marker text "▌" is what style.go renders when SelectMark is
	// applied via lipgloss BorderLeft. We assert the styled cell is
	// present near the last turn's text.
	lastIdx := strings.Index(out, "120 bytes")
	if lastIdx < 0 {
		t.Fatalf("can't find last turn in output")
	}
	if !strings.Contains(out[:lastIdx], "▌") {
		t.Errorf("selection mark glyph not present before last turn")
	}
}

func TestViewHidesComposerInReadMode(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice", Read: true}).WithTurns(seedTurns())
	m.width, m.height = 80, 24
	out := m.View()
	if strings.Contains(out, "i to edit") {
		t.Errorf("read mode should not show composer hint: %q", out)
	}
	if !strings.Contains(out, "READ") {
		t.Errorf("read mode pill missing: %q", out)
	}
}
```

Append to `pkg/tui/chat/view_test.go` (create the file).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/tui/chat/... -run "TestChatTUIRendersTurnsAndAttachesToolCalls|TestViewHidesComposerInReadMode" -v`
Expected: FAIL (View method or its body undefined).

- [ ] **Step 3: Implement view.go**

```go
package chat

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	selectMarkGlyph = "▌"
	actorUserGlyph  = "›"
	actorAgentGlyph = "●"
	toolLineGlyph   = "⚡"
)

// View renders the full TUI: header, stream, composer (hidden in
// read mode), status bar. Layout-only; every accent comes from
// m.styles role tokens.
func (m Model) View() string {
	header := m.renderHeader()
	stream := m.renderStream()
	status := m.renderStatusBar()
	if m.opts.Read {
		return strings.Join([]string{header, stream, status}, "\n")
	}
	composer := m.renderComposer()
	return strings.Join([]string{header, stream, composer, status}, "\n")
}

func (m Model) renderHeader() string {
	name := m.styles.HeaderName.Render(m.opts.AgentName)
	branch := ""
	if m.opts.Branch != "" {
		branch = " " + m.styles.HeaderBranch.Render("⎇ "+m.opts.Branch)
	}
	return name + branch
}

func (m Model) renderStream() string {
	var rows []string
	for i, t := range m.turns {
		rows = append(rows, m.renderTurn(i, t))
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderTurn(idx int, t Turn) string {
	mark := "  "
	if idx == m.selection {
		mark = m.styles.SelectMark.Render(selectMarkGlyph) + " "
	}
	ts := m.styles.Muted.Render(t.Ts.Format("15:04:05"))
	var actor, glyph string
	switch t.Actor {
	case ActorUser:
		glyph = m.styles.ActorUser.Render(actorUserGlyph)
		actor = m.styles.ActorUser.Render("you   ")
	case ActorAgent:
		glyph = m.styles.ActorAgent.Render(actorAgentGlyph)
		actor = m.styles.ActorAgent.Render(m.opts.AgentName)
	case ActorSystem:
		glyph = m.styles.Muted.Render("·")
		actor = m.styles.Muted.Render("system")
	default:
		glyph = " "
		actor = m.styles.Muted.Render("?     ")
	}
	head := fmt.Sprintf("%s%s  %s %s  %s", mark, ts, glyph, actor, t.Text)
	if len(t.ToolCalls) == 0 {
		return head
	}
	var b strings.Builder
	b.WriteString(head)
	for _, tc := range t.ToolCalls {
		b.WriteString("\n")
		b.WriteString(m.renderToolLine(tc))
	}
	return b.String()
}

func (m Model) renderToolLine(tc ToolCall) string {
	statusStyle := m.styles.ToolLine
	statusWord := "pending"
	switch tc.Status {
	case ToolStatusOK:
		statusStyle = m.styles.Success
		statusWord = "ok"
	case ToolStatusFailed:
		statusStyle = m.styles.Destructive
		statusWord = "failed"
	}
	dur := ""
	if tc.Duration > 0 {
		dur = fmt.Sprintf(" · %s", tc.Duration.Truncate(1e6).String())
	}
	arg := ""
	if tc.Arg != "" {
		arg = fmt.Sprintf(" [%s]", tc.Arg)
	}
	line := fmt.Sprintf("      %s %s%s %s%s",
		m.styles.ToolLine.Render(toolLineGlyph),
		m.styles.ToolLine.Render(tc.Name),
		m.styles.ToolLine.Render(arg),
		statusStyle.Render(statusWord),
		m.styles.ToolLine.Render(dur),
	)
	return line
}

func (m Model) renderComposer() string {
	// Task 6 wires a real textarea; for now render a parked placeholder
	// so view tests in this task still work end-to-end.
	hint := m.styles.Muted.Render("i to edit")
	parked := m.styles.ComposerParked.Render("(composer)")
	return lipgloss.JoinHorizontal(lipgloss.Top, parked, "  ", hint)
}

func (m Model) renderStatusBar() string {
	left := ""
	switch {
	case m.opts.Read:
		left = m.styles.StatusRead.Render(" READ ")
	case m.mode == ModeInsert:
		left = m.styles.StatusInsert.Render("INSERT")
	default:
		left = m.styles.StatusNormal.Render("NORMAL")
	}
	right := ""
	if len(m.turns) > 0 {
		right = fmt.Sprintf("turn %d / %d", m.selection+1, len(m.turns))
	}
	return left + "  " + m.styles.Muted.Render(right)
}
```

Write to `pkg/tui/chat/view.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/tui/chat/... -run "TestChatTUIRendersTurnsAndAttachesToolCalls|TestViewHidesComposerInReadMode" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/tui/chat/view.go pkg/tui/chat/view_test.go
git commit -m "chat-tui: render header + turns + tool-call lines + selection mark"
```

---

## Task 5: Viewport + selection-centered scroll

**Files:**
- Modify: `pkg/tui/chat/model.go` (add viewport field + offset logic)
- Modify: `pkg/tui/chat/view.go` (use viewport bounds for stream slicing)
- Modify: `pkg/tui/chat/model_test.go` (add scroll tests)

- [ ] **Step 1: Add the failing scroll test**

Append to `pkg/tui/chat/model_test.go`:

```go
func TestSelectionCenteredScroll(t *testing.T) {
	t.Parallel()
	// 20 turns, viewport height of 5 stream rows. Selection at the end
	// should clamp scrollOffset so the selection is at the bottom; moving
	// selection up by several rows should center it.
	turns := make([]Turn, 20)
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	for i := range turns {
		turns[i] = Turn{Ts: t0.Add(time.Duration(i) * time.Second), Actor: ActorAgent, Text: "row"}
	}
	m := New(Options{AgentName: "alice"}).WithTurns(turns)
	m.width, m.height = 80, 11 // header(1) + stream(5) + composer(1) + status(1) + padding(3) ≈ 11
	m.streamHeight = 5

	// Selection is at last index (19); first visible row should be 15.
	if got := m.streamFirstVisible(); got != 15 {
		t.Errorf("first visible at end: want 15, got %d", got)
	}

	// Move selection to index 10. With selection-centered scroll and
	// height=5, the selected row should sit at the middle index (2),
	// so first visible = 10 - 2 = 8.
	m.selection = 10
	if got := m.streamFirstVisible(); got != 8 {
		t.Errorf("first visible at middle: want 8, got %d", got)
	}

	// Move selection to index 1 (near top). first visible must clamp
	// to >= 0.
	m.selection = 1
	if got := m.streamFirstVisible(); got != 0 {
		t.Errorf("first visible near top: want 0, got %d", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/... -run TestSelectionCenteredScroll -v`
Expected: FAIL (streamHeight / streamFirstVisible undefined).

- [ ] **Step 3: Add streamHeight + streamFirstVisible to model.go**

In `pkg/tui/chat/model.go`, add to the `Model` struct (before `width int`):

```go
	streamHeight int // rows of the stream area; computed from height in View
```

Add this method to the bottom of `model.go`:

```go
// streamFirstVisible returns the index of the first turn that should
// render in the stream viewport, given the current selection and
// streamHeight. Implements spec §"Selection-centered scroll": the
// selected turn lands in the middle of the viewport when navigation
// moves it off-screen; clamps to [0, len(turns)-streamHeight].
func (m Model) streamFirstVisible() int {
	if m.streamHeight <= 0 || len(m.turns) <= m.streamHeight {
		return 0
	}
	mid := m.streamHeight / 2
	first := m.selection - mid
	if first < 0 {
		first = 0
	}
	maxFirst := len(m.turns) - m.streamHeight
	if first > maxFirst {
		first = maxFirst
	}
	return first
}
```

- [ ] **Step 4: Update renderStream to slice by viewport**

Replace the body of `renderStream` in `pkg/tui/chat/view.go` with:

```go
func (m Model) renderStream() string {
	if m.streamHeight <= 0 {
		// No layout yet — render everything (tests without a configured
		// streamHeight still see the full transcript).
		var rows []string
		for i, t := range m.turns {
			rows = append(rows, m.renderTurn(i, t))
		}
		return strings.Join(rows, "\n")
	}
	first := m.streamFirstVisible()
	last := first + m.streamHeight
	if last > len(m.turns) {
		last = len(m.turns)
	}
	var rows []string
	for i := first; i < last; i++ {
		rows = append(rows, m.renderTurn(i, m.turns[i]))
	}
	return strings.Join(rows, "\n")
}
```

- [ ] **Step 5: Compute streamHeight when the window is sized**

In `Update`, replace the `tea.WindowSizeMsg` arm with:

```go
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// header(1) + composer(1, hidden in read mode) + status(1) + 1 spacing
		reserved := 3
		if m.opts.Read {
			reserved = 2
		}
		m.streamHeight = msg.Height - reserved
		if m.streamHeight < 1 {
			m.streamHeight = 1
		}
		return m, nil
```

- [ ] **Step 6: Run the tests**

Run: `go test ./pkg/tui/chat/... -v`
Expected: PASS (all earlier tests still pass; new scroll test passes).

- [ ] **Step 7: Commit**

```bash
git add pkg/tui/chat/model.go pkg/tui/chat/view.go pkg/tui/chat/model_test.go
git commit -m "chat-tui: selection-centered scroll + windowed stream rendering"
```

---

## Task 6: INSERT mode + composer (bubbles/textarea) + draft preservation

**Files:**
- Modify: `pkg/tui/chat/model.go` (composer field, INSERT-mode arm in Update)
- Modify: `pkg/tui/chat/view.go` (active composer rendering)
- Modify: `pkg/tui/chat/model_test.go` (mode-transition + draft-preservation tests)

This task covers spec acceptance `TestChatTUIModeTransitions` (steps 1-6 minus send dispatch — send lands in Task 7) and `TestChatTUIPreservesDraftAcrossModeFlips`.

- [ ] **Step 1: Add the failing tests**

Append to `pkg/tui/chat/model_test.go`:

```go
func TestNormalI_EntersInsertMode(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, key("i"))
	if m.Mode() != ModeInsert {
		t.Errorf("after i: want INSERT, got %v", m.Mode())
	}
}

func TestInsertEsc_ReturnsToNormalPreservingDraft(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = applyKey(t, m, key("i"))
	for _, r := range "hold on" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = applyKey(t, m, key("esc"))
	if m.Mode() != ModeNormal {
		t.Errorf("after esc: want NORMAL, got %v", m.Mode())
	}
	if got := m.Draft(); got != "hold on" {
		t.Errorf("draft preserved: want %q, got %q", "hold on", got)
	}
	// Re-enter INSERT and append more
	m = applyKey(t, m, key("i"))
	for _, r := range " — wait" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.Draft(); got != "hold on — wait" {
		t.Errorf("draft appended: want %q, got %q", "hold on — wait", got)
	}
}

func TestInsertJ_TypesCharacterNotNavigation(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	selBefore := m.Selection()
	m = applyKey(t, m, key("i"))
	m = applyKey(t, m, key("j"))
	if got := m.Draft(); got != "j" {
		t.Errorf("draft after j in INSERT: want %q, got %q", "j", got)
	}
	if m.Selection() != selBefore {
		t.Errorf("selection moved in INSERT: was %d, now %d", selBefore, m.Selection())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/tui/chat/... -run "TestNormalI_EntersInsertMode|TestInsertEsc_ReturnsToNormalPreservingDraft|TestInsertJ_TypesCharacterNotNavigation" -v`
Expected: FAIL.

- [ ] **Step 3: Add composer to the Model struct**

In `pkg/tui/chat/model.go`, update imports:

```go
import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)
```

Add to the `Model` struct (after `gPending`):

```go
	composer textarea.Model
```

Update `New` to initialize the textarea:

```go
func New(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "type a message"
	ta.CharLimit = 0
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Prompt = "▎ "
	ta.Blur()
	return Model{
		opts:     opts,
		mode:     ModeNormal,
		styles:   defaultStyles(),
		keys:     defaultKeys(),
		composer: ta,
	}
}
```

Add the `Draft` getter:

```go
// Draft returns the current composer text. Exposed for tests.
func (m Model) Draft() string { return m.composer.Value() }
```

Update `Update` to dispatch by mode and re-resize the composer:

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		reserved := 3
		if m.opts.Read {
			reserved = 2
		}
		m.streamHeight = msg.Height - reserved
		if m.streamHeight < 1 {
			m.streamHeight = 1
		}
		m.composer.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
		if m.mode == ModeInsert {
			return m.updateInsert(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}
```

In `updateNormal`, add an `i` arm that enters INSERT mode. After the existing `case key.Matches(msg, m.keys.NormalTop):` block, insert:

```go
	case !m.opts.Read && key.Matches(msg, m.keys.NormalInsert):
		m.mode = ModeInsert
		m.composer.Focus()
		return m, textarea.Blink
```

Add `updateInsert`:

```go
func (m Model) updateInsert(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.InsertExit):
		m.mode = ModeNormal
		m.composer.Blur()
		return m, nil
	case key.Matches(msg, m.keys.InsertSend):
		// Send dispatch lands in Task 7; for now treat enter like a
		// no-op so the test that types text + esc still sees the draft
		// preserved.
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}
```

- [ ] **Step 4: Render the active composer in view.go**

Replace `renderComposer` with:

```go
func (m Model) renderComposer() string {
	if m.mode == ModeInsert {
		return m.styles.ComposerActive.Render(m.composer.View())
	}
	hint := m.styles.Muted.Render("i to edit")
	parked := m.styles.ComposerParked.Render(m.composer.View())
	return parked + "  " + hint
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/tui/chat/... -v`
Expected: PASS (all prior + 3 new tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/tui/chat/model.go pkg/tui/chat/view.go pkg/tui/chat/model_test.go
git commit -m "chat-tui: INSERT mode + textarea composer with draft preservation"
```

---

## Task 7: Send path — Enter dispatches the draft, clears composer, bounces to NORMAL

**Files:**
- Modify: `pkg/tui/chat/model.go` (send hook + Enter handling + local echo of user prompt)
- Modify: `pkg/tui/chat/model_test.go` (send test)

The MVP spec says (Open Q3): "Ship the bounce — send, return to base." So `Enter` in INSERT must (a) dispatch the draft to the send hook, (b) clear the composer, (c) flip mode back to NORMAL, (d) put a local-echo user Turn into the stream so the operator's prompt shows up immediately. The selection lands on that new last turn.

- [ ] **Step 1: Write the failing send test**

Append to `pkg/tui/chat/model_test.go`:

```go
func TestInsertEnter_SendsDraftAndBouncesToNormal(t *testing.T) {
	t.Parallel()
	var sent []string
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = m.WithSendHook(func(text string) { sent = append(sent, text) })
	m = applyKey(t, m, key("i"))
	for _, r := range "hello" {
		m = applyKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = applyKey(t, m, key("enter"))
	if len(sent) != 1 || sent[0] != "hello" {
		t.Errorf("sent: want [hello], got %v", sent)
	}
	if m.Mode() != ModeNormal {
		t.Errorf("after enter: want NORMAL, got %v", m.Mode())
	}
	if got := m.Draft(); got != "" {
		t.Errorf("draft after send: want empty, got %q", got)
	}
	turns := m.Turns()
	if len(turns) != len(seedTurns())+1 {
		t.Fatalf("turn count: want %d, got %d", len(seedTurns())+1, len(turns))
	}
	last := turns[len(turns)-1]
	if last.Actor != ActorUser || last.Text != "hello" {
		t.Errorf("last turn: %+v", last)
	}
	if m.Selection() != len(turns)-1 {
		t.Errorf("selection: want %d, got %d", len(turns)-1, m.Selection())
	}
}

func TestInsertEnter_EmptyDraftIsNoop(t *testing.T) {
	t.Parallel()
	var sent []string
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m = m.WithSendHook(func(text string) { sent = append(sent, text) })
	m = applyKey(t, m, key("i"))
	m = applyKey(t, m, key("enter"))
	if len(sent) != 0 {
		t.Errorf("empty draft sent: %v", sent)
	}
	if m.Mode() != ModeInsert {
		t.Errorf("empty enter stayed in INSERT? mode=%v", m.Mode())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/... -run TestInsertEnter -v`
Expected: FAIL (WithSendHook undefined).

- [ ] **Step 3: Add the send hook to model.go**

In `pkg/tui/chat/model.go`, add a `SendFunc` type and a field to `Model`:

```go
// SendFunc is the callback the model invokes when the operator hits
// Enter in INSERT. The receiver is responsible for dispatching the
// prompt_agent RPC; program.go wires this against pkg/client.
type SendFunc func(text string)
```

Add to the `Model` struct (after `composer`):

```go
	send SendFunc
```

Add the builder:

```go
// WithSendHook installs the callback invoked on INSERT-Enter. Returns
// the model so callers can chain it with WithTurns.
func (m Model) WithSendHook(fn SendFunc) Model {
	m.send = fn
	return m
}
```

In `updateInsert`, replace the `InsertSend` arm with:

```go
	case key.Matches(msg, m.keys.InsertSend):
		text := strings.TrimSpace(m.composer.Value())
		if text == "" {
			return m, nil
		}
		if m.send != nil {
			m.send(text)
		}
		m.composer.SetValue("")
		// Local echo: surface the operator's prompt as a user turn so
		// the conversation reads naturally before the daemon's frame
		// round-trips back. Selection lands on the new last turn.
		m.turns = append(m.turns, Turn{Ts: time.Now(), Actor: ActorUser, Text: text})
		m.selection = len(m.turns) - 1
		m.mode = ModeNormal
		m.composer.Blur()
		return m, nil
```

Add `strings` and `time` to the imports in `model.go`:

```go
import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/tui/chat/... -v`
Expected: PASS (all prior + 2 new tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/tui/chat/model.go pkg/tui/chat/model_test.go
git commit -m "chat-tui: INSERT-Enter dispatches send hook, locally echoes user turn"
```

---

## Task 8: Frames subscription as tea.Cmd (frames.go)

**Files:**
- Create: `pkg/tui/chat/frames.go`
- Modify: `pkg/tui/chat/model.go` (handle frameMsg + lifecycleMsg in Update; expose Init that pumps the channels)
- Create: `pkg/tui/chat/frames_test.go`

- [ ] **Step 1: Write the failing test (frame arrival appends a turn)**

```go
package chat

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestFrameMsgAppendsTurn(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice"})
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{
		Ts:        t0,
		FrameKind: sextantproto.FrameAssistantText,
		Body:      map[string]any{"text": "hi from agent"},
	}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if got := len(mm.Turns()); got != 1 {
		t.Fatalf("turn count: want 1, got %d", got)
	}
	if mm.Turns()[0].Text != "hi from agent" {
		t.Errorf("text: %q", mm.Turns()[0].Text)
	}
}

func TestFrameMsgHoldsSelectionWhenScrolledUp(t *testing.T) {
	t.Parallel()
	// Operator is on turn 1 of 4 (scrolled up). A new frame arrives.
	// Selection must stay where the operator parked it (spec §"Open
	// Q5": hold position).
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	m.selection = 1
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{Ts: t0, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": "later"}}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if mm.Selection() != 1 {
		t.Errorf("selection moved: want 1, got %d", mm.Selection())
	}
	if got := len(mm.Turns()); got != len(seedTurns())+1 {
		t.Errorf("turn count: want %d, got %d", len(seedTurns())+1, got)
	}
}

func TestFrameMsgAdvancesSelectionWhenAtBottom(t *testing.T) {
	t.Parallel()
	// Operator is on the last turn (auto-tail). New frame arrives —
	// selection follows so auto-tail keeps tracking.
	m := New(Options{AgentName: "alice"}).WithTurns(seedTurns())
	// selection is already at len-1 from WithTurns
	prev := m.Selection()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	msg := frameMsg{Frame: Frame{Ts: t0, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": "next"}}}
	next, _ := m.Update(msg)
	mm := next.(Model)
	if mm.Selection() != prev+1 {
		t.Errorf("auto-tail: want %d, got %d", prev+1, mm.Selection())
	}
}
```

Write to `pkg/tui/chat/frames_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/... -run TestFrameMsg -v`
Expected: FAIL.

- [ ] **Step 3: Implement frames.go**

```go
package chat

import (
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// frameMsg is dispatched when a new agent frame arrives on the NATS
// subscription. The reducer appends it to the turn list via the same
// FramesToTurns rules used for the seed transcript.
type frameMsg struct {
	Frame Frame
}

// lifecycleMsg is dispatched when a lifecycle envelope arrives. The
// reducer uses Transition for status-bar indicators and "ended" to
// surface a closing UI hint (Task 10 implements the auto-close on
// --tail).
type lifecycleMsg struct {
	Payload sextantproto.LifecyclePayload
}

// subscriptionEndedMsg fires when one of the source channels closes
// (typically because the upstream client.Client was Close()'d). The
// reducer logs it to the status bar and stops pumping.
type subscriptionEndedMsg struct {
	Source string // "frames" or "lifecycle"
}

// pumpFrames returns a tea.Cmd that blocks on one message from `ch`
// and dispatches it as a frameMsg. The reducer re-issues this Cmd
// after every receive so the program keeps draining the channel.
func pumpFrames(ch <-chan client.Message) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return subscriptionEndedMsg{Source: "frames"}
		}
		if msg.Err != nil {
			// Skip undecodable frames silently; re-issue ourselves.
			return pumpFrames(ch)()
		}
		return frameMsg{Frame: messageToFrame(msg)}
	}
}

// pumpLifecycle is the lifecycle-channel counterpart.
func pumpLifecycle(ch <-chan client.Message) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return subscriptionEndedMsg{Source: "lifecycle"}
		}
		if msg.Err != nil {
			return pumpLifecycle(ch)()
		}
		var p sextantproto.LifecyclePayload
		if err := json.Unmarshal(msg.Envelope.Payload, &p); err != nil {
			return pumpLifecycle(ch)()
		}
		return lifecycleMsg{Payload: p}
	}
}

// messageToFrame decodes a client.Message into our Frame intake type.
// Errors fall through to a system-note frame with the raw payload so
// nothing is silently dropped from the operator's view.
func messageToFrame(msg client.Message) Frame {
	var fp sextantproto.AgentFramePayload
	if err := json.Unmarshal(msg.Envelope.Payload, &fp); err != nil {
		return Frame{
			Ts:        msg.Envelope.Ts,
			FrameKind: sextantproto.FrameSystemNote,
			Body:      map[string]any{"text": "(undecodable frame)"},
		}
	}
	return Frame{
		Ts:        msg.Envelope.Ts,
		FrameKind: fp.FrameKind,
		ToolName:  fp.ToolName,
		Body:      fp.Body,
	}
}

// _ = context.Background — context is imported for future shape
// parity with client.Client methods; keep the import live.
var _ = context.Background
```

Write to `pkg/tui/chat/frames.go`.

- [ ] **Step 4: Handle the messages in Update**

In `pkg/tui/chat/model.go`, extend `Update` to handle the new messages. After the `tea.KeyMsg` arm:

```go
	case frameMsg:
		atBottom := m.selection == len(m.turns)-1 || len(m.turns) == 0
		// Synthesize a one-frame slice and feed it through the same
		// collapser used at seed time. We append the produced turn(s)
		// rather than rebuilding from scratch so existing turn objects
		// (with their tool-call indices) stay stable.
		newTurns := FramesToTurns(append(framesFromTurns(m.turns), msg.Frame))
		// If the only delta is an attached tool-call line on the last
		// agent turn, newTurns has the same length as before. Otherwise
		// at least one new turn was appended.
		grew := len(newTurns) > len(m.turns)
		m.turns = newTurns
		if grew && atBottom {
			m.selection = len(m.turns) - 1
		}
		if m.selection > len(m.turns)-1 {
			m.selection = len(m.turns) - 1
		}
		return m, nil
	case lifecycleMsg:
		// Future status-bar hooks (live/paused) land here. For MVP we
		// just absorb it; --tail close lives in program.go.
		_ = msg
		return m, nil
	case subscriptionEndedMsg:
		// Upstream channel closed — usually the daemon went away or the
		// operator hit Ctrl-C. Treat as quit signal.
		return m, tea.Quit
```

Add a helper at the bottom of `model.go`:

```go
// framesFromTurns reconstructs an approximate Frame slice from a turn
// slice for the incremental-append path in Update(frameMsg). The
// shape is lossy (Body maps aren't preserved) but FramesToTurns only
// needs Actor/Text/FrameKind/ToolName/Ts to reconstitute the same
// turn structure for already-rendered rows.
func framesFromTurns(turns []Turn) []Frame {
	var frames []Frame
	for _, t := range turns {
		switch t.Actor {
		case ActorUser:
			frames = append(frames, Frame{Ts: t.Ts, Actor: ActorUser, Text: t.Text})
		case ActorAgent:
			frames = append(frames, Frame{Ts: t.Ts, FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"text": t.Text}})
			for _, tc := range t.ToolCalls {
				frames = append(frames, Frame{
					Ts: tc.StartTs, FrameKind: sextantproto.FrameToolCall,
					ToolName: tc.Name, Body: map[string]any{"path": tc.Arg},
				})
				if tc.Status != ToolStatusPending {
					body := map[string]any{}
					if tc.Status == ToolStatusFailed {
						body["error"] = "boom"
					}
					frames = append(frames, Frame{
						Ts: tc.StartTs.Add(tc.Duration), FrameKind: sextantproto.FrameToolResult,
						ToolName: tc.Name, Body: body,
					})
				}
			}
		case ActorSystem:
			frames = append(frames, Frame{Ts: t.Ts, FrameKind: sextantproto.FrameSystemNote, Body: map[string]any{"text": t.Text}})
		}
	}
	return frames
}
```

Add `"github.com/love-lena/sextant/pkg/sextantproto"` to the imports of `model.go`.

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/tui/chat/... -v`
Expected: PASS (all prior + 3 new tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/tui/chat/frames.go pkg/tui/chat/frames_test.go pkg/tui/chat/model.go
git commit -m "chat-tui: frame/lifecycle subscription pump + Update handlers"
```

---

## Task 9: Read mode — INSERT unreachable, READ pill, composer hidden

**Files:**
- Modify: `pkg/tui/chat/model.go` (gate the `i` arm — already done in Task 6, but add explicit test)
- Modify: `pkg/tui/chat/model_test.go` (acceptance test)

This task covers spec acceptance `TestChatTUIReadModeDisallowsInsert`. Most of the gating already exists from Task 6 (`!m.opts.Read &&` guard) and Task 4 (READ pill in renderStatusBar, composer hidden in View). This task locks the behavior with an explicit acceptance test.

- [ ] **Step 1: Write the failing acceptance test**

Append to `pkg/tui/chat/model_test.go`:

```go
func TestChatTUIReadModeDisallowsInsert(t *testing.T) {
	t.Parallel()
	m := New(Options{AgentName: "alice", Read: true}).WithTurns(seedTurns())
	m.width, m.height = 80, 24
	// 'i' must be a no-op in read mode.
	m = applyKey(t, m, key("i"))
	if m.Mode() != ModeNormal {
		t.Errorf("read mode after i: want NORMAL, got %v", m.Mode())
	}
	out := m.View()
	if !strings.Contains(out, "READ") {
		t.Errorf("status bar missing READ pill: %q", out)
	}
	if strings.Contains(out, "i to edit") {
		t.Errorf("composer hint should not appear in read mode: %q", out)
	}
}
```

Add `"strings"` to the imports of `model_test.go` if not already present.

- [ ] **Step 2: Run the test to verify it passes (the gating already exists)**

Run: `go test ./pkg/tui/chat/... -run TestChatTUIReadModeDisallowsInsert -v`
Expected: PASS — the `!m.opts.Read &&` guard from Task 6 and the read-mode branch in `View()` from Task 4 already cover this.

If it fails because the View output doesn't contain "READ" or the `i to edit` hint is leaking, fix the corresponding branches in `view.go` and re-run.

- [ ] **Step 3: Commit**

```bash
git add pkg/tui/chat/model_test.go
git commit -m "chat-tui: lock read-mode behavior with acceptance test"
```

---

## Task 10: Program entry point (program.go) — Run, send hook, --tail close

**Files:**
- Create: `pkg/tui/chat/program.go`
- Create: `pkg/tui/chat/program_test.go`

The `program.go` file owns the `tea.Program` lifecycle and the bus interface used to talk to `pkg/client`. It is the only file `cmd/sextant/conversation.go` will import. The send hook here is what `cmd/sextant/conversation.go` wires against the real `prompt_agent` RPC.

- [ ] **Step 1: Write a failing fake-bus test for the send pipeline**

```go
package chat

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type fakeBus struct {
	sent []string
}

func (f *fakeBus) SendPrompt(_ context.Context, _ uuid.UUID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func TestSendHookCallsBusSendPrompt(t *testing.T) {
	t.Parallel()
	bus := &fakeBus{}
	id := uuid.New()
	hook := makeSendHook(context.Background(), bus, id)
	hook("hello world")
	if len(bus.sent) != 1 || bus.sent[0] != "hello world" {
		t.Errorf("bus sent: %v", bus.sent)
	}
}
```

Write to `pkg/tui/chat/program_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/tui/chat/... -run TestSendHook -v`
Expected: FAIL (makeSendHook undefined).

- [ ] **Step 3: Implement program.go**

```go
package chat

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
)

// Bus is the surface program.go needs from a pkg/client.Client. Defined
// as an interface so tests can wire a fake without booting NATS.
type Bus interface {
	SendPrompt(ctx context.Context, agent uuid.UUID, text string) error
}

// RunConfig collects every parameter Run needs. The frames/lifecycle
// channels are owned by the caller (cmd/sextant/conversation.go) so it
// can apply the same WithStartSeq seeding as the NDJSON path.
type RunConfig struct {
	Ctx        context.Context
	Bus        Bus
	AgentID    uuid.UUID
	AgentName  string
	Branch     string
	Read       bool
	TailUntilEnd bool
	Frames     <-chan client.Message
	Lifecycle  <-chan client.Message
	// SeedTurns is an optional initial transcript. cmd/sextant/conversation.go
	// uses --from-seq to backfill these before starting the program.
	SeedTurns []Turn
}

// Run constructs the tea.Program, wires the send hook + subscription
// pumps, and blocks until the program exits.
func Run(cfg RunConfig) error {
	m := New(Options{
		AgentName: cfg.AgentName,
		Branch:    cfg.Branch,
		Read:      cfg.Read,
	})
	if len(cfg.SeedTurns) > 0 {
		m = m.WithTurns(cfg.SeedTurns)
	}
	if !cfg.Read {
		m = m.WithSendHook(makeSendHook(cfg.Ctx, cfg.Bus, cfg.AgentID))
	}
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(cfg.Ctx))
	// Pump frames + lifecycle as initial commands. The reducer re-issues
	// each pump after every receive in Task 8's Update arms via the
	// model's stored channel — wired through Init when SeedSubscription
	// is set; here we kick the pumps explicitly.
	go func() {
		for cmd := range frameCmdLoop(cfg.Frames) {
			prog.Send(cmd())
		}
	}()
	go func() {
		for cmd := range lifecycleCmdLoop(cfg.Lifecycle) {
			prog.Send(cmd())
		}
	}()
	_, err := prog.Run()
	if err != nil {
		return fmt.Errorf("chat tui: %w", err)
	}
	return nil
}

// frameCmdLoop yields a fresh pumpFrames cmd after every prior receive,
// turning the channel-pump into a sequence of one-shot tea.Cmds the
// goroutine in Run can forward to the program.
func frameCmdLoop(ch <-chan client.Message) <-chan tea.Cmd {
	out := make(chan tea.Cmd)
	go func() {
		defer close(out)
		for {
			out <- pumpFrames(ch)
			// Block until the next iteration is requested by the consumer
			// reading from `out`. The consumer reads exactly one cmd per
			// iteration; pumpFrames blocks on the channel, so the chain
			// naturally advances one frame at a time.
		}
	}()
	return out
}

func lifecycleCmdLoop(ch <-chan client.Message) <-chan tea.Cmd {
	out := make(chan tea.Cmd)
	go func() {
		defer close(out)
		for {
			out <- pumpLifecycle(ch)
		}
	}()
	return out
}

// makeSendHook returns a SendFunc that publishes the prompt via the
// Bus's SendPrompt method. Errors are swallowed — they'll surface on
// the next frame the daemon emits (or the lack of one), and the TUI
// stays alive.
func makeSendHook(ctx context.Context, bus Bus, id uuid.UUID) SendFunc {
	return func(text string) {
		_ = bus.SendPrompt(ctx, id, text)
	}
}

// clientBus adapts *client.Client to the Bus interface. Lives here so
// the rest of the package never imports pkg/client.
type clientBus struct {
	cli *client.Client
}

// NewClientBus wraps a live client for use with Run. Exposed so
// cmd/sextant/conversation.go can build a Bus from its existing
// *client.Client.
func NewClientBus(cli *client.Client) Bus { return &clientBus{cli: cli} }

func (b *clientBus) SendPrompt(ctx context.Context, id uuid.UUID, text string) error {
	// Mirrors cmd/sextant/ask.go: subscribe-before-publish is the caller's
	// job (we're already streaming frames); here we just dispatch the
	// prompt_agent RPC.
	return sendPromptRPC(ctx, b.cli, id, text)
}
```

Write to `pkg/tui/chat/program.go`.

- [ ] **Step 4: Add the sendPromptRPC helper (kept separate so tests don't need pkg/rpc)**

Append to `pkg/tui/chat/program.go`:

```go
// sendPromptRPC is split out so we can call it via the Bus seam in
// tests without dragging pkg/rpc imports into the test file.
func sendPromptRPC(ctx context.Context, cli *client.Client, id uuid.UUID, text string) error {
	req := struct {
		AgentID uuid.UUID `json:"agent_id"`
		Content string    `json:"content"`
	}{AgentID: id, Content: text}
	var resp struct{ OK bool `json:"ok"` }
	if err := cli.RPC(ctx, "prompt_agent", req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("prompt_agent: daemon returned ok=false")
	}
	return nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/tui/chat/... -run TestSendHook -v`
Expected: PASS.

- [ ] **Step 6: Run the full package test suite**

Run: `go test ./pkg/tui/chat/... -v`
Expected: PASS (everything green).

- [ ] **Step 7: Commit**

```bash
git add pkg/tui/chat/program.go pkg/tui/chat/program_test.go
git commit -m "chat-tui: program.Run with bus seam + frame/lifecycle pump loops"
```

---

## Task 11: Tool-call status color test (acceptance)

**Files:**
- Modify: `pkg/tui/chat/view_test.go` (add the acceptance test)

This task covers spec acceptance `TestChatTUIToolCallStatusColor`.

- [ ] **Step 1: Write the failing acceptance test**

Append to `pkg/tui/chat/view_test.go`:

```go
func TestChatTUIToolCallStatusColor(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	frames := []Frame{
		envAt(t0, sextantproto.FrameAssistantText, map[string]any{"text": "running"}, ""),
		envAt(t0.Add(time.Second), sextantproto.FrameToolCall, map[string]any{"path": "a"}, "good_tool"),
		envAt(t0.Add(2*time.Second), sextantproto.FrameToolResult, map[string]any{"ok": true}, "good_tool"),
		envAt(t0.Add(3*time.Second), sextantproto.FrameToolCall, map[string]any{"path": "b"}, "bad_tool"),
		envAt(t0.Add(4*time.Second), sextantproto.FrameToolResult, map[string]any{"error": "boom"}, "bad_tool"),
	}
	m := New(Options{AgentName: "alice"}).WithTurns(FramesToTurns(frames))
	m.width, m.height = 80, 24

	styles := defaultStyles()
	okStr := styles.Success.Render("ok")
	failStr := styles.Destructive.Render("failed")

	out := m.View()
	if !strings.Contains(out, okStr) {
		t.Errorf("ok tool call missing success-styled token: rendered=%q", out)
	}
	if !strings.Contains(out, failStr) {
		t.Errorf("failed tool call missing destructive-styled token: rendered=%q", out)
	}
}
```

Add to view_test.go imports: `"time"` and `"github.com/love-lena/sextant/pkg/sextantproto"` (already there for the earlier test).

- [ ] **Step 2: Run the test**

Run: `go test ./pkg/tui/chat/... -run TestChatTUIToolCallStatusColor -v`
Expected: PASS — `renderToolLine` from Task 4 already routes through `styles.Success` / `styles.Destructive` based on `tc.Status`. If it fails, the assertion is testing that the *exact* styled bytes are present; double-check `renderToolLine` is applying the role-token styles (not the muted style) for non-pending statuses.

- [ ] **Step 3: Commit**

```bash
git add pkg/tui/chat/view_test.go
git commit -m "chat-tui: assert tool-call status uses success/destructive role tokens"
```

---

## Task 12: Wire into `cmd/sextant/conversation.go`

**Files:**
- Modify: `cmd/sextant/conversation.go` (launch TUI by default; `--json` keeps NDJSON; add `--read`)
- Create: `cmd/sextant/conversation_test.go` (acceptance test for CLI dispatch)

This task covers spec acceptance `TestSextantConversationLaunchesTUIByDefault`. Because the TUI requires a real TTY to draw, the test asserts the dispatch decision (which branch was taken) rather than booting a tea.Program. We expose the dispatch via a package-level seam that tests override.

- [ ] **Step 1: Write the failing CLI dispatch test**

```go
package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
)

// fakeChatRunner records the dispatch path so the test can assert
// which branch runConversationDispatch took without booting bubbletea.
type fakeChatRunner struct {
	called    int
	gotReadFn bool
	read      bool
}

func (f *fakeChatRunner) Run(
	_ context.Context,
	_ io.Writer,
	_ *client.Client,
	_ <-chan client.Message,
	_ <-chan client.Message,
	_ uuid.UUID,
	read, asJSON, tail bool,
) error {
	f.called++
	f.gotReadFn = true
	f.read = read
	if asJSON {
		return errors.New("fakeChatRunner: asJSON should never reach here")
	}
	_ = tail
	return nil
}

func TestSextantConversationLaunchesTUIByDefault(t *testing.T) {
	// Default path (no --json, no --read) selects the TUI dispatch.
	prev := chatRunner
	defer func() { chatRunner = prev }()
	fake := &fakeChatRunner{}
	chatRunner = fake

	frames := make(chan client.Message)
	lifecycle := make(chan client.Message)
	close(frames)
	close(lifecycle)

	id := uuid.New()
	err := runConversationDispatch(context.Background(), nil, nil, frames, lifecycle, id, false /*read*/, false /*asJSON*/, false /*tail*/)
	if err != nil {
		t.Fatalf("dispatch returned err: %v", err)
	}
	if fake.called != 1 {
		t.Errorf("chat runner not called: %d", fake.called)
	}
	if fake.read {
		t.Errorf("read flag should be false by default")
	}
}

func TestSextantConversationJsonStaysOnNdjsonPath(t *testing.T) {
	prev := chatRunner
	defer func() { chatRunner = prev }()
	fake := &fakeChatRunner{}
	chatRunner = fake

	frames := make(chan client.Message)
	lifecycle := make(chan client.Message)
	close(frames)
	close(lifecycle)

	id := uuid.New()
	err := runConversationDispatch(context.Background(), io.Discard, nil, frames, lifecycle, id, false, true /*asJSON*/, false)
	if err != nil {
		t.Fatalf("dispatch err: %v", err)
	}
	if fake.called != 0 {
		t.Errorf("chat runner should not be called for --json: called=%d", fake.called)
	}
}

func TestSextantConversationReadPropagates(t *testing.T) {
	prev := chatRunner
	defer func() { chatRunner = prev }()
	fake := &fakeChatRunner{}
	chatRunner = fake

	frames := make(chan client.Message)
	lifecycle := make(chan client.Message)
	close(frames)
	close(lifecycle)

	id := uuid.New()
	err := runConversationDispatch(context.Background(), nil, nil, frames, lifecycle, id, true /*read*/, false, false)
	if err != nil {
		t.Fatalf("dispatch err: %v", err)
	}
	if !fake.read {
		t.Errorf("read flag dropped")
	}
}
```

Write to `cmd/sextant/conversation_test.go`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/sextant/... -run "TestSextantConversation" -v`
Expected: FAIL (chatRunner / runConversationDispatch undefined).

- [ ] **Step 3: Refactor conversation.go to expose the dispatch seam**

In `cmd/sextant/conversation.go`:

a) Add new imports:

```go
	"github.com/love-lena/sextant/pkg/tui/chat"
```

b) Add the `--read` flag and a TTY/JSON branch. Replace the body of `runConversation` from `subject := "agents." + ...` through `return streamConversation(...)` with:

```go
	var read bool
	fs.BoolVar(&read, "read", false, "open the chat TUI without a composer (read-only)")
	// re-parse: --read needs to be registered before parseCommonOpts; if
	// it's already been parsed in Step (a)'s flag-set rewiring this is a
	// no-op. (See note below.)
	_ = read

	subject := "agents." + id.String() + ".frames"
	frameOpts := []client.SubscribeOption{}
	if fromSeq > 0 {
		frameOpts = append(frameOpts, client.WithStartSeq(fromSeq))
	}
	frames, err := cli.Subscribe(ctx, subject, frameOpts...)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	lifecycle, err := cli.Subscribe(ctx, "agents."+id.String()+".lifecycle")
	if err != nil {
		return fmt.Errorf("subscribe lifecycle: %w", err)
	}
	return runConversationDispatch(ctx, os.Stdout, cli, frames, lifecycle, id, read, opts.asJSON, tail)
```

c) Move the `--read` BoolVar declaration *up* with the existing `--tail` / `--from-seq` declarations so it's registered before `parseCommonOpts(fs, args)`. The final declaration block should read:

```go
	var tail bool
	var fromSeq uint64
	var read bool
	fs.BoolVar(&tail, "tail", false, "exit on the next lifecycle.ended for this agent")
	fs.Uint64Var(&fromSeq, "from-seq", 0, "resume from JetStream stream sequence N")
	fs.BoolVar(&read, "read", false, "open the chat TUI without a composer (read-only)")
	opts, rest, err := parseCommonOpts(fs, args)
```

d) Add the dispatch function and the swappable runner seam at the bottom of `conversation.go`:

```go
// chatRunnerIface lets tests substitute a fake for the heavy
// bubbletea-bound runner. Production: chatRunnerFunc(chat.Run).
type chatRunnerIface interface {
	Run(
		ctx context.Context,
		w io.Writer,
		cli *client.Client,
		frames <-chan client.Message,
		lifecycle <-chan client.Message,
		id uuid.UUID,
		read, asJSON, tail bool,
	) error
}

type chatRunnerFunc func(
	context.Context, io.Writer, *client.Client,
	<-chan client.Message, <-chan client.Message,
	uuid.UUID, bool, bool, bool,
) error

func (f chatRunnerFunc) Run(
	ctx context.Context, w io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, asJSON, tail bool,
) error {
	return f(ctx, w, cli, frames, lifecycle, id, read, asJSON, tail)
}

// chatRunner is the swappable seam. Tests overwrite it.
var chatRunner chatRunnerIface = chatRunnerFunc(realChatRun)

// realChatRun is the production dispatch into pkg/tui/chat.Run. The
// branch shape (asJSON falls through to streamConversation) lives in
// runConversationDispatch — this function is only invoked for the
// TUI path.
func realChatRun(
	ctx context.Context, _ io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, _ bool, tail bool,
) error {
	return chat.Run(chat.RunConfig{
		Ctx:          ctx,
		Bus:          chat.NewClientBus(cli),
		AgentID:      id,
		AgentName:    id.String(), // TODO: resolve agent name via list_agents
		Read:         read,
		TailUntilEnd: tail,
		Frames:       frames,
		Lifecycle:    lifecycle,
	})
}

// runConversationDispatch routes between the NDJSON streamer and the
// chat TUI. Lives at top level (not inside runConversation) so tests
// can call it with a fake chatRunner without re-parsing flags.
func runConversationDispatch(
	ctx context.Context, w io.Writer, cli *client.Client,
	frames <-chan client.Message, lifecycle <-chan client.Message,
	id uuid.UUID, read, asJSON, tail bool,
) error {
	if asJSON {
		return streamConversation(ctx, w, frames, lifecycle, id, asJSON, tail)
	}
	return chatRunner.Run(ctx, w, cli, frames, lifecycle, id, read, asJSON, tail)
}
```

e) Add to existing imports at the top of `conversation.go`:

```go
	"io"
	"github.com/love-lena/sextant/pkg/client"
```

(check what's already imported — `io` and `pkg/client` likely already are.)

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/sextant/... -run "TestSextantConversation" -v`
Expected: PASS (all three dispatch tests).

- [ ] **Step 5: Smoke-build the binary**

Run: `go build ./cmd/sextant/...`
Expected: no errors.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS (no regressions in any existing test).

- [ ] **Step 7: Update the conversationUsage string**

In `conversation.go`, update `conversationUsage` to mention the new flags:

```go
const conversationUsage = `usage: sextant conversation <agent_uuid> [--tail] [--from-seq N] [--json] [--read]

Open the modal chat TUI on agents.<uuid>.frames + agents.<uuid>.lifecycle.
--json switches to the legacy NDJSON streamer (preserved byte-identical
for piped consumers). --read opens the same TUI without a composer.
--tail closes the window on the next lifecycle.ended event.`
```

- [ ] **Step 8: Commit**

```bash
git add cmd/sextant/conversation.go cmd/sextant/conversation_test.go
git commit -m "sextant: launch chat TUI by default for `sextant conversation`"
```

---

## Self-review checklist (completed inline by the plan author)

**Spec coverage:**

| Spec acceptance test | Plan task |
|---|---|
| TestChatTUIRendersTurnsAndAttachesToolCalls | Task 4 |
| TestChatTUIModeTransitions | Tasks 3, 6, 7 |
| TestChatTUIReadModeDisallowsInsert | Task 9 |
| TestSextantConversationLaunchesTUIByDefault | Task 12 |
| TestChatTUIPreservesDraftAcrossModeFlips | Task 6 |
| TestChatTUIToolCallStatusColor | Task 11 |

Open questions from the spec are answered in code per the spec's "ship X" directives — initial NORMAL with last turn selected (Task 3), Esc-only INSERT exit (Task 6), send bounces to NORMAL (Task 7), hold position on new frames (Task 8). Streaming-token text and the scroll-anchoring hint are explicitly deferred.

**Type/name consistency:**

- `Frame` (turn.go) is the single intake type used everywhere.
- `Turn` / `Actor` / `ToolStatus` / `ToolCall` names are consistent across turn.go, model.go, view.go, frames.go.
- `Model.Mode()` / `Model.Selection()` / `Model.Turns()` / `Model.Draft()` are the test-facing getters; all four are introduced in Tasks 3 (Mode/Selection/Turns) and 6 (Draft).
- `SendFunc` (model.go) is what `WithSendHook` accepts and what `makeSendHook` returns.
- `Bus` (program.go) has one method `SendPrompt`; `NewClientBus` adapts `*client.Client` to it.
- `RunConfig` field names match what `cmd/sextant/conversation.go` populates in Task 12.

**Placeholder scan:** no "TBD", no "implement later", no "appropriate error handling", no "similar to Task N" — each task shows the exact code the engineer types.

---

## Notes for the executor

- The package is the precedent for `pkg/tui/<surface>/` — co-locate audit/pending/etc TUIs as siblings of `chat/` (spec §"Implementation shape").
- The existing `cmd/sextant-tui-agents/` is a separate binary, not a precedent for this package layout. The chat TUI is library-shaped because it's launched as a *subcommand* of `sextant`, not a standalone binary.
- Local agent-name resolution in Task 12's `realChatRun` uses the UUID string as a placeholder for the header. A follow-up issue (not part of this plan) should resolve the friendly name via `list_agents` RPC at startup — left as a `TODO:` in the code for visibility.
- `bubbles/key.Help` (mentioned in spec §"Implementation shape") is *not* wired in this MVP because the status bar already shows only the active-mode keys per the spec's "no busy legend" rule. If a help overlay is wanted later, add it as a deferred follow-up.
