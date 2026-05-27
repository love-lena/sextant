package fixtures

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Fixture is one named, deterministic snapshot of the bus state a TUI
// would see at startup. Fixtures are read-only — Get returns a copy of
// the registered fixture and the fake bus only ever projects over it.
type Fixture struct {
	// Name is the lookup key — `fixtures.Get("demo")` returns the Demo
	// fixture. Lowercase, no whitespace.
	Name string

	// Agents is the canned list_agents response. Order is preserved so
	// renderings are deterministic.
	Agents []sextantproto.AgentSummary

	// Conversations is per-agent transcripts. Keys are
	// AgentSummary.UUID values; missing keys render as an empty
	// transcript when the chat TUI opens against that agent.
	Conversations map[uuid.UUID][]Frame

	// Pending is the snapshot of unanswered user_input requests. The
	// pending-list command surfaces this directly; the agents TUI uses
	// the count for its status bar.
	Pending []sextantproto.UserInputRequestPayload

	// Operator is the operator name reported back by ui_state.* KV
	// reads against the fake bus. Empty string means "unnamed".
	Operator string
}

// Frame is the primitive transcript element a fixture stores. It maps
// 1:1 onto sextantproto.AgentFramePayload (with an Actor override so
// user-side prompts can be expressed without a real envelope kind).
//
// Helpers ToEnvelopes / ToChatFrames adapt this into the shapes the
// fake bus and the chat package consume.
type Frame struct {
	// Ts is the wall-clock time the frame would have landed on the bus.
	// Used verbatim as the envelope timestamp and as the chat-side
	// Frame.Ts.
	Ts time.Time

	// Actor is the originator — "user", "agent", or "system". Empty
	// string falls back to FrameKind-derived classification (matching
	// pkg/tui/chat behavior).
	Actor Actor

	// FrameKind matches the AgentFramePayload field. Required when
	// Actor != ActorUser; for ActorUser frames the kind is left blank
	// and the text is supplied directly via Text.
	FrameKind sextantproto.FrameKind

	// ToolName is the SDK-reported tool identifier for FrameToolCall /
	// FrameToolResult frames. Ignored otherwise.
	ToolName string

	// Body is the payload field map verbatim. Conventionally
	// {"text": "..."} for assistant/system frames and a small free-form
	// map for tool calls and results.
	Body map[string]any

	// Text is a shortcut for {"text": ...} that the preview binary and
	// chat adapters prefer over Body for user-originated frames.
	Text string
}

// Actor mirrors chat.Actor without forcing fixtures to depend on the
// chat package. Stringified into the AgentFramePayload only when the
// FrameKind doesn't already determine the actor.
type Actor int

const (
	// ActorUnknown lets FrameKind decide.
	ActorUnknown Actor = iota
	// ActorUser frames are operator prompts — they carry Text and no
	// FrameKind.
	ActorUser
	// ActorAgent frames are SDK output. Most fixture frames sit here.
	ActorAgent
	// ActorSystem frames are sextant-side notes (lifecycle banners,
	// errors).
	ActorSystem
)

// registry holds every named fixture. Populated by package-level var
// initialization (not init()) so the registration order is explicit
// and easy to grep for.
var registry = buildRegistry()

func buildRegistry() map[string]Fixture {
	r := map[string]Fixture{}
	for _, f := range []Fixture{buildDemo()} {
		if f.Name == "" {
			panic("fixtures: empty Name in registry")
		}
		if _, dup := r[f.Name]; dup {
			panic("fixtures: duplicate name " + f.Name)
		}
		r[f.Name] = f
	}
	return r
}

// Get returns the fixture registered under name. The ok return is
// false when no such fixture exists; callers should surface the list
// of registered names back to the operator.
func Get(name string) (Fixture, bool) {
	f, ok := registry[name]
	return f, ok
}

// Names returns the registered fixture names in lexicographic order.
// Used by --fixture help text and error messages.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// MustGet is the panicking variant of Get. Used in tests that hard-
// code a fixture name; never in command paths.
func MustGet(name string) Fixture {
	f, ok := Get(name)
	if !ok {
		panic(fmt.Sprintf("fixtures.MustGet: no fixture %q (have %v)", name, Names()))
	}
	return f
}

// AgentByName looks up an agent in the fixture by its friendly name.
// Used by tests and adapters that want to address a specific agent
// without typing its UUID.
func (f Fixture) AgentByName(name string) (sextantproto.AgentSummary, bool) {
	for _, a := range f.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return sextantproto.AgentSummary{}, false
}

// FirstAgent returns the first agent in the fixture or the zero value
// when the fixture has none. Convenience for preview binaries that
// open against "the demo agent".
func (f Fixture) FirstAgent() sextantproto.AgentSummary {
	if len(f.Agents) == 0 {
		return sextantproto.AgentSummary{}
	}
	return f.Agents[0]
}
