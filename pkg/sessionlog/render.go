package sessionlog

import (
	"fmt"
	"strings"
)

// Mode is a context view mode. Raw is the floor (verbatim line); the
// typed modes filter/reformat the stream. Shared by the CLI dump
// (`sextant agents context`) and the TUI (`pkg/tui/context`) so both
// render identically.
type Mode string

const (
	ModeRaw          Mode = "raw"
	ModeConversation Mode = "conversation"
	ModeTools        Mode = "tools"
	ModeThinking     Mode = "thinking"
	ModeUsage        Mode = "usage"
	ModeTree         Mode = "tree"
)

// AllModes lists modes in canonical order (also the TUI's 1–6 key order).
var AllModes = []Mode{ModeRaw, ModeConversation, ModeTools, ModeThinking, ModeUsage, ModeTree}

// ParseMode validates a --mode string (empty → ModeRaw).
func ParseMode(v string) (Mode, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ModeRaw, nil
	}
	for _, m := range AllModes {
		if string(m) == v {
			return m, nil
		}
	}
	legal := make([]string, len(AllModes))
	for i, m := range AllModes {
		legal[i] = string(m)
	}
	return "", fmt.Errorf("invalid mode %q (legal: %s)", v, strings.Join(legal, ", "))
}

// UsageAccumulator carries running token totals across ModeUsage lines.
// Stateful: the caller keeps one per stream.
type UsageAccumulator struct {
	turn        int
	totalCreate int
	totalRead   int
}

// RenderLine formats ev under mode. Returns "" when the event has
// nothing to show in that mode. ModeUsage requires a non-nil acc.
func RenderLine(ev Event, mode Mode, acc *UsageAccumulator) string {
	switch mode {
	case ModeRaw:
		return string(ev.RawLine())
	case ModeConversation:
		return renderConversation(ev)
	case ModeTools:
		return renderTools(ev)
	case ModeThinking:
		return renderThinking(ev)
	case ModeUsage:
		return renderUsage(ev, acc)
	case ModeTree:
		return renderTree(ev)
	default:
		return string(ev.RawLine())
	}
}

func renderConversation(ev Event) string {
	var lines []string
	switch m := ev.(type) {
	case UserMessage:
		if m.Text != "" {
			return "user: " + m.Text
		}
		for _, b := range m.ContentBlocks {
			if tr, ok := b.(ToolResultBlock); ok {
				lines = append(lines, toolResultLine("tool_result", tr))
			}
		}
	case AssistantMessage:
		for _, b := range m.ContentBlocks {
			switch bb := b.(type) {
			case TextBlock:
				lines = append(lines, "assistant: "+bb.Text)
			case ToolUseBlock:
				lines = append(lines, fmt.Sprintf("tool_use[%s] %s %s", bb.ID, bb.Name, OneLine(string(bb.Input))))
			}
		}
	case SystemMessage:
		return fmt.Sprintf("system[%s]", m.Subtype)
	}
	return strings.Join(lines, "\n")
}

func renderTools(ev Event) string {
	var lines []string
	switch m := ev.(type) {
	case AssistantMessage:
		for _, b := range m.ContentBlocks {
			if tu, ok := b.(ToolUseBlock); ok {
				lines = append(lines, fmt.Sprintf("call %s %s %s", tu.ID, tu.Name, OneLine(string(tu.Input))))
			}
		}
	case UserMessage:
		for _, b := range m.ContentBlocks {
			if tr, ok := b.(ToolResultBlock); ok {
				lines = append(lines, toolResultLine("result", tr))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func toolResultLine(label string, tr ToolResultBlock) string {
	marker := "ok"
	if tr.IsError {
		marker = "ERR"
	}
	body := tr.Text
	if body == "" && len(tr.Blocks) > 0 {
		body = fmt.Sprintf("(%d sub-block(s))", len(tr.Blocks))
	}
	return fmt.Sprintf("%s[%s] %s: %s", label, marker, tr.ToolUseID, OneLine(body))
}

func renderThinking(ev Event) string {
	m, ok := ev.(AssistantMessage)
	if !ok {
		return ""
	}
	var lines []string
	for _, b := range m.ContentBlocks {
		if th, ok := b.(ThinkingBlock); ok {
			lines = append(lines, fmt.Sprintf("thinking[%s]: %s", m.UUID, OneLine(th.Thinking)))
		}
	}
	return strings.Join(lines, "\n")
}

func renderUsage(ev Event, acc *UsageAccumulator) string {
	if acc == nil {
		return ""
	}
	m, ok := ev.(AssistantMessage)
	if !ok {
		return ""
	}
	u := m.Usage
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheReadInputTokens == 0 {
		return ""
	}
	acc.turn++
	acc.totalCreate += u.CacheCreationInputTokens
	acc.totalRead += u.CacheReadInputTokens
	ratio := 0.0
	if denom := acc.totalCreate + acc.totalRead; denom > 0 {
		ratio = float64(acc.totalRead) / float64(denom)
	}
	return fmt.Sprintf(
		"turn=%d in=%d out=%d cache_create=%d (5m=%d 1h=%d) cache_read=%d hit=%.2f model=%s stop=%s",
		acc.turn, u.InputTokens, u.OutputTokens,
		u.CacheCreationInputTokens,
		u.CacheCreation.Ephemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens,
		u.CacheReadInputTokens, ratio, m.Model, m.StopReason,
	)
}

func renderTree(ev Event) string {
	c := ev.Common()
	if c.UUID == "" {
		return ""
	}
	marker := "main"
	if c.IsSidechain {
		marker = "sidechain"
	}
	return fmt.Sprintf("[%s] %s parent=%s kind=%s", marker, c.UUID, c.ParentUUID, ev.Kind())
}

// OneLine collapses whitespace runs into single spaces and truncates so
// a 200 KiB tool_result can't blow out one render line.
func OneLine(s string) string {
	const limit = 240
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	out := make([]byte, 0, len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			if !prevSpace {
				out = append(out, ' ')
				prevSpace = true
			}
			continue
		}
		out = append(out, string(r)...)
		prevSpace = r == ' '
	}
	folded := string(out)
	if len(folded) > limit {
		folded = folded[:limit] + "…"
	}
	return folded
}
