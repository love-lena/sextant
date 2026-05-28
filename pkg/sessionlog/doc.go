// Package sessionlog is a typed JSONL parser for Claude Code SDK
// session files (the `~/.claude/projects/<cwd>/<sessionId>.jsonl`
// stream the Claude Agent SDK writes during a `query()` loop).
//
// It is the parsing layer underneath
// `sextant agents context <agent>` and the future `-i` TUI mount.
// Pure parsing + types — no Bubble Tea, no sextant internals, no
// daemon RPC. Reusable from the CLI dump path and the TUI alike.
//
// # Stream semantics
//
// Stream returns a channel that emits one Event per line of the JSONL
// reader. The reader is consumed line-by-line via bufio.Scanner with
// an enlarged buffer (some SDK lines carry 100KiB+ tool outputs). The
// caller is expected to supply an io.Reader that may block (for
// example, github.com/nxadm/tail's reader) so the channel can serve
// both the "dump once and exit" CLI mode and the "tail forever"
// follow mode without two code paths.
//
// The channel closes when the underlying reader returns io.EOF *and*
// the reader does not block further. For tail-style readers that
// never EOF, the caller closes the underlying reader (e.g. via the
// tail handle's Stop()) to signal end-of-stream.
//
// # Discriminated record types
//
// Each JSONL line carries a `type` field. We model the closed set of
// types we care about:
//
//   - "assistant"  → AssistantMessage (Usage, Model, StopReason, blocks)
//   - "user"       → UserMessage (string prompts and tool_result returns)
//   - "system"     → SystemMessage
//
// Everything else — `mode`, `queue-operation`, `ai-title`, etc. — falls
// through to Raw so the operator can still see it in the dump. Unknown
// is the floor; the typed events are filters on top of that floor.
//
// Errors per line are surfaced as RawEvent with a non-nil ParseError so
// one malformed line cannot abort the stream. Filters in higher layers
// can decide whether to highlight, suppress, or count them.
package sessionlog
