package chat

import (
	"sort"
	"strings"
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
	// open queues unresolved tool calls per tool name. A FIFO queue
	// (rather than a single slot) is what lets concurrent same-name
	// tool calls — e.g. two `search` invocations in one turn — each
	// match their own tool_result in arrival order.
	type openCall struct {
		turn    int
		callIdx int
	}
	open := map[string][]openCall{}
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
			turns = append(turns, Turn{Ts: f.Ts, Actor: actor, Text: text})
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
			open[f.ToolName] = append(open[f.ToolName], openCall{turn: ti, callIdx: len(turns[ti].ToolCalls) - 1})
		case f.FrameKind == sextantproto.FrameToolResult:
			queue := open[f.ToolName]
			if len(queue) == 0 {
				continue
			}
			oc := queue[0]
			if len(queue) == 1 {
				delete(open, f.ToolName)
			} else {
				open[f.ToolName] = queue[1:]
			}
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
			if text == "" {
				// Last-ditch: render a flattened body summary so we never drop
				// a system note silently. If even the summary is empty, skip
				// the frame entirely — empty rows are pure noise in the chat.
				text = summarizeSystemBody(f.Body)
			}
			if text == "" {
				continue
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

// summarizeSystemBody is a last-ditch text extractor for system_note /
// error frames that don't follow the "text" / "message" key convention.
// Flattens up to ~3 string fields into "k=v" pairs, sorted by key for
// determinism. Returns "" if nothing useful surfaces.
func summarizeSystemBody(body map[string]any) string {
	if len(body) == 0 {
		return ""
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if v, ok := body[k].(string); ok && v != "" {
			parts = append(parts, k+"="+v)
		}
		if len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// summarizeArg picks one salient field from a tool-call body to render
// inline next to the tool name. Prefers "path", then "command", then
// "url", then "query"; falls back to the alphabetically-first
// string-valued field. Sorted fallback is what keeps the rendered
// row stable across rerenders — map iteration order is randomized,
// and FramesToTurns runs on every model update.
func summarizeArg(body map[string]any) string {
	for _, k := range []string{"path", "command", "url", "query"} {
		if s, ok := body[k].(string); ok && s != "" {
			return s
		}
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := body[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
