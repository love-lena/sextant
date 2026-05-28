package sessionlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// MaxLineBytes is the per-line cap used by Stream's internal
// bufio.Scanner. Real SDK lines comfortably exceed bufio's default
// 64KiB ceiling — a single Read tool_result can spill a 200KiB+ file
// blob — so we raise it. Anything beyond this cap is split and the
// truncated tail surfaces as a parse error via RawEvent.
const MaxLineBytes = 8 * 1024 * 1024

// Stream consumes r line-by-line and emits one Event per JSONL line
// onto the returned channel. The channel is buffered just enough that
// a slow consumer doesn't gate the reader; the goroutine closes the
// channel when the reader returns io.EOF or an unrecoverable error.
//
// # Tail behavior
//
// When r blocks (e.g. github.com/nxadm/tail's reader), the scanner
// blocks too; Stream simply waits. The caller closes the underlying
// reader to terminate the goroutine.
//
// # Cancellation
//
// Pass a context-bound reader (or close the reader yourself) to
// cancel the loop. Stream itself does not take a context — its sole
// job is to project bytes into events, which keeps it composable with
// any reader shape.
func Stream(r io.Reader) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), MaxLineBytes)
		for scanner.Scan() {
			line := scanner.Bytes()
			// scanner.Bytes() returns a slice into the scanner's
			// internal buffer; copy before handing off so subsequent
			// Scan() calls don't clobber the event's RawLine.
			cp := make([]byte, len(line))
			copy(cp, line)
			ch <- ParseLine(cp)
		}
		// bufio.ErrTooLong manifests when a line exceeds MaxLineBytes.
		// Surface it as a parse error so the operator sees the truncated
		// line in raw mode rather than silent stream-end.
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			ch <- RawEvent{ParseError: err}
		}
	}()
	return ch
}

// ParseLine projects one JSONL line into a typed Event. Exported so
// tests can exercise the projection without spinning a goroutine, and
// so view-mode renderers can re-parse a stashed RawLine on demand.
//
// Empty / whitespace-only lines decode to a zero-valued RawEvent
// rather than an error — the SDK doesn't write blank lines, but
// concatenated transcripts sometimes carry them and surfacing them
// as parse errors would be needlessly noisy.
func ParseLine(line []byte) Event {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return RawEvent{raw: line}
	}

	// First pass: read just the discriminator + wrapper fields. We
	// need the `type` to decide which typed struct to decode into,
	// and the common wrapper fields populate every Event.
	var disc struct {
		commonRaw
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &disc); err != nil {
		return RawEvent{
			raw:        line,
			ParseError: err,
		}
	}

	common := disc.toCommon()

	switch disc.Type {
	case KindAssistant:
		return parseAssistant(line, common)
	case KindUser:
		return parseUser(line, common)
	case KindSystem:
		return parseSystem(line, common)
	default:
		return RawEvent{
			CommonFields: common,
			kind:         disc.Type,
			raw:          line,
		}
	}
}

// rawAssistant is the on-the-wire shape of an assistant record's
// message field. We decode into this and then project into the
// typed AssistantMessage. RawMessage for content + diagnostics so we
// can re-walk the block array without redoing the outer unmarshal.
type rawAssistant struct {
	CommonRequestID string          `json:"requestId"`
	Message         rawAssistantMsg `json:"message"`
}

type rawAssistantMsg struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      json.RawMessage `json:"usage"`
	Content    json.RawMessage `json:"content"`
}

func parseAssistant(line []byte, common CommonFields) Event {
	var ra rawAssistant
	if err := json.Unmarshal(line, &ra); err != nil {
		return RawEvent{
			CommonFields: common,
			kind:         KindAssistant,
			raw:          line,
			ParseError:   err,
		}
	}

	out := AssistantMessage{
		CommonFields: common,
		Model:        ra.Message.Model,
		StopReason:   ra.Message.StopReason,
		MessageID:    ra.Message.ID,
		RequestID:    ra.CommonRequestID,
		raw:          line,
	}
	if len(ra.Message.Usage) > 0 {
		// best-effort — older sessions have a thinner usage shape.
		// Errors are silently ignored: the rest of the record is
		// still useful.
		_ = json.Unmarshal(ra.Message.Usage, &out.Usage)
	}
	if len(ra.Message.Content) > 0 {
		out.ContentBlocks = parseContentBlocks(ra.Message.Content)
	}
	return out
}

// rawUser is the on-the-wire shape of a user record. message.content
// is either a string (operator prompt) or an array (tool_result
// returns); we keep it as RawMessage and branch in the projector.
type rawUser struct {
	PromptID string     `json:"promptId"`
	Message  rawUserMsg `json:"message"`
}

type rawUserMsg struct {
	Content json.RawMessage `json:"content"`
}

func parseUser(line []byte, common CommonFields) Event {
	var ru rawUser
	if err := json.Unmarshal(line, &ru); err != nil {
		return RawEvent{
			CommonFields: common,
			kind:         KindUser,
			raw:          line,
			ParseError:   err,
		}
	}
	out := UserMessage{
		CommonFields: common,
		PromptID:     ru.PromptID,
		raw:          line,
	}
	content := bytes.TrimSpace(ru.Message.Content)
	if len(content) == 0 {
		return out
	}
	switch content[0] {
	case '"':
		// Plain string content. json.Unmarshal handles escapes.
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			out.Text = s
		}
	case '[':
		out.ContentBlocks = parseContentBlocks(content)
	default:
		// Some unexpected shape — keep the raw line, leave Text and
		// ContentBlocks empty. RawLine() still works.
	}
	return out
}

type rawSystem struct {
	Subtype string `json:"subtype"`
	Level   string `json:"level"`
}

func parseSystem(line []byte, common CommonFields) Event {
	var rs rawSystem
	if err := json.Unmarshal(line, &rs); err != nil {
		return RawEvent{
			CommonFields: common,
			kind:         KindSystem,
			raw:          line,
			ParseError:   err,
		}
	}
	return SystemMessage{
		CommonFields: common,
		Subtype:      rs.Subtype,
		Level:        rs.Level,
		raw:          line,
	}
}

// parseContentBlocks walks an `[{...},{...},...]` array and projects
// each element into the appropriate Block impl. Unknown block types
// fall through to RawBlock so the raw view still has something to
// render.
func parseContentBlocks(raw json.RawMessage) []Block {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]Block, 0, len(items))
	for _, item := range items {
		out = append(out, parseBlock(item))
	}
	return out
}

func parseBlock(raw json.RawMessage) Block {
	var disc struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &disc); err != nil {
		return RawBlock{TypeName: "", Raw: raw}
	}
	switch disc.Type {
	case BlockTypeText:
		var t struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return RawBlock{TypeName: disc.Type, Raw: raw}
		}
		return TextBlock{Text: t.Text}
	case BlockTypeThinking:
		var th struct {
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(raw, &th); err != nil {
			return RawBlock{TypeName: disc.Type, Raw: raw}
		}
		return ThinkingBlock{Thinking: th.Thinking, Signature: th.Signature}
	case BlockTypeToolUse:
		var tu struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(raw, &tu); err != nil {
			return RawBlock{TypeName: disc.Type, Raw: raw}
		}
		return ToolUseBlock{ID: tu.ID, Name: tu.Name, Input: tu.Input}
	case BlockTypeToolResult:
		return parseToolResultBlock(raw)
	default:
		return RawBlock{TypeName: disc.Type, Raw: raw}
	}
}

func parseToolResultBlock(raw json.RawMessage) Block {
	var tr struct {
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return RawBlock{TypeName: BlockTypeToolResult, Raw: raw}
	}
	out := ToolResultBlock{
		ToolUseID: tr.ToolUseID,
		IsError:   tr.IsError,
	}
	content := bytes.TrimSpace(tr.Content)
	switch {
	case len(content) == 0:
		return out
	case content[0] == '"':
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			out.Text = s
		}
	case content[0] == '[':
		out.Blocks = parseContentBlocks(content)
	default:
		// keep zero-valued; RawLine of the parent record still preserved
	}
	return out
}
