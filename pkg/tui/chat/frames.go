package chat

import (
	"encoding/json"
	"time"

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
//
// Ts carries the envelope's wire timestamp so the header can render a
// relative-time suffix on terminal states (`ended (12m ago)`). Zero
// value when the source envelope wasn't available (e.g. test paths
// that construct lifecycleMsg directly) — renderers must fall back
// gracefully.
type lifecycleMsg struct {
	Payload sextantproto.LifecyclePayload
	Ts      time.Time
}

// subscriptionEndedMsg fires when one of the source channels closes
// (typically because the upstream client.Client was Close()'d). The
// reducer treats this as a quit signal.
type subscriptionEndedMsg struct {
	Source string // "frames" or "lifecycle"
}

// restartFailedMsg is dispatched by the restart hook (program.go's
// makeRestartHook) when restart_agent returns an RPC error. The
// reducer stores Err in Model.lastError so the host can render an
// inline banner above the stream — see
// [[feat-tui-chat-restart-error-banner]] for the rationale.
type restartFailedMsg struct {
	Err string
}

// restartErrorTimeoutMsg is the self-issued tea.Tick that auto-clears
// the banner after restartErrorTTL. Seq matches the Model.errorSeq the
// tick was scheduled against; if the seq has since changed (i.e. a
// second restart failure overwrote the banner) the tick is a stale
// no-op.
type restartErrorTimeoutMsg struct {
	Seq int
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
		_ = msg.Ack()
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
		_ = msg.Ack()
		return lifecycleMsg{Payload: p, Ts: msg.Envelope.Ts.Time}
	}
}

// messageToFrame decodes a client.Message into our Frame intake type.
// Errors fall through to a system-note frame with a placeholder body so
// nothing is silently dropped from the operator's view.
func messageToFrame(msg client.Message) Frame {
	var fp sextantproto.AgentFramePayload
	if err := json.Unmarshal(msg.Envelope.Payload, &fp); err != nil {
		return Frame{
			Ts:        msg.Envelope.Ts.Time,
			FrameKind: sextantproto.FrameSystemNote,
			Body:      map[string]any{"text": "(undecodable frame)"},
		}
	}
	return Frame{
		Ts:        msg.Envelope.Ts.Time,
		FrameKind: fp.FrameKind,
		ToolName:  fp.ToolName,
		Body:      fp.Body,
	}
}
