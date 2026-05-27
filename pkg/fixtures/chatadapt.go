package fixtures

import (
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/tui/chat"
)

// ChatFrames adapts a fixture's transcript for the agent identified
// by id into the shape pkg/tui/chat consumes. The Actor mapping is
// 1:1 (fixtures.ActorX → chat.ActorX); Text and Body fall through
// verbatim.
//
// Returns nil when the fixture has no transcript for id, which the
// chat preview binary renders as an empty conversation.
func ChatFrames(f Fixture, id uuid.UUID) []chat.Frame {
	src := f.Conversations[id]
	if len(src) == 0 {
		return nil
	}
	out := make([]chat.Frame, 0, len(src))
	for _, s := range src {
		out = append(out, chat.Frame{
			Ts:        s.Ts,
			FrameKind: s.FrameKind,
			ToolName:  s.ToolName,
			Body:      s.Body,
			Actor:     adaptActor(s.Actor),
			Text:      s.Text,
		})
	}
	return out
}

func adaptActor(a Actor) chat.Actor {
	switch a {
	case ActorUser:
		return chat.ActorUser
	case ActorAgent:
		return chat.ActorAgent
	case ActorSystem:
		return chat.ActorSystem
	default:
		return chat.ActorUnknown
	}
}
