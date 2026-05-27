package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Bus is the in-memory fake the --fixture flag wires into TUI entry
// points in place of a real *client.Client.
//
// It implements the consumer-side interfaces TUI components use today:
// RPC for list_agents / get_agent_status, Subscribe for per-agent
// frames + lifecycle channels, WatchKV / GetKV / PutKV for ui_state.
// SendPrompt is exposed so chat.Bus is satisfied without dragging the
// full client surface in.
//
// Construct via NewBus(Fixture). The returned Bus is safe for use by
// one TUI program at a time; concurrent calls share the same fixture
// snapshot but each Subscribe gets its own channel.
type Bus struct {
	fx Fixture

	mu       sync.Mutex
	closed   bool
	prompts  []SentPrompt // observed SendPrompt calls; tests inspect
	kvState  map[string]map[string][]byte
	channels []chan client.Message
	kvWatch  []chan client.KVUpdate
}

// SentPrompt records one observed SendPrompt call. Tests assert
// against bus.SentPrompts() after driving the TUI.
type SentPrompt struct {
	Agent uuid.UUID
	Text  string
	At    time.Time
}

// NewBus returns a Bus serving f. The fixture is copied by reference;
// fixtures are read-only by convention so the lack of a deep copy is
// fine.
func NewBus(f Fixture) *Bus {
	return &Bus{
		fx:      f,
		kvState: map[string]map[string][]byte{},
	}
}

// Fixture returns the fixture this bus is serving.
func (b *Bus) Fixture() Fixture { return b.fx }

// Close releases every outstanding Subscribe / WatchKV channel. After
// Close, further Subscribe / WatchKV calls return ErrClosed.
func (b *Bus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, ch := range b.channels {
		close(ch)
	}
	b.channels = nil
	for _, ch := range b.kvWatch {
		close(ch)
	}
	b.kvWatch = nil
	return nil
}

// SentPrompts returns a copy of the prompts observed via SendPrompt so
// tests can assert against them.
func (b *Bus) SentPrompts() []SentPrompt {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]SentPrompt, len(b.prompts))
	copy(out, b.prompts)
	return out
}

// SendPrompt implements chat.Bus. Records the call; never errors.
func (b *Bus) SendPrompt(_ context.Context, agent uuid.UUID, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prompts = append(b.prompts, SentPrompt{Agent: agent, Text: text, At: time.Now()})
	return nil
}

// RPC dispatches in-memory replies for the verbs TUI surfaces depend
// on. Unknown verbs return a wrapped error so a misuse surfaces
// loudly rather than hanging the program.
func (b *Bus) RPC(_ context.Context, verb string, req, resp any, _ ...client.RPCOption) error {
	if b.isClosed() {
		return client.ErrClosed
	}
	switch verb {
	case rpc.VerbListAgents:
		r, ok := resp.(*sextantproto.ListAgentsResponse)
		if !ok {
			return fmt.Errorf("fixtures: RPC %s: resp not *ListAgentsResponse", verb)
		}
		filter, _ := req.(sextantproto.ListAgentsRequest)
		r.Agents = filterAgents(b.fx.Agents, filter.Filter)
		return nil
	case rpc.VerbGetAgentStatus:
		r, ok := resp.(*sextantproto.GetAgentStatusResponse)
		if !ok {
			return fmt.Errorf("fixtures: RPC %s: resp not *GetAgentStatusResponse", verb)
		}
		gr, _ := req.(sextantproto.GetAgentStatusRequest)
		for _, a := range b.fx.Agents {
			if a.UUID == gr.AgentID {
				r.Status = sextantproto.AgentStatus{
					UUID:      a.UUID,
					Name:      a.Name,
					Lifecycle: a.Lifecycle,
					Version:   a.Version,
					UpdatedAt: a.UpdatedAt,
				}
				return nil
			}
		}
		return fmt.Errorf("fixtures: agent %s not in fixture", gr.AgentID)
	case "prompt_agent":
		// chat.Bus uses this verb via client.RPC. We don't wire the
		// model here — the chat path uses SendPrompt directly instead.
		// Returning OK keeps any direct callers happy.
		if r, ok := resp.(*struct {
			OK bool `json:"ok"`
		}); ok {
			r.OK = true
		}
		return nil
	default:
		return fmt.Errorf("fixtures: RPC %s: not implemented", verb)
	}
}

// Subscribe returns a channel pre-populated with the canned envelopes
// for the requested subject and then held open. Supported subjects:
//
//   - agents.<uuid>.frames     — per-agent frame transcript
//   - agents.<uuid>.lifecycle  — empty (no lifecycle events in the fixture)
//   - agents.*.lifecycle       — empty fan-in
//   - user_input.>             — pending-request snapshot, then idle
//
// Unknown subjects return an open, idle channel so TUI startup paths
// that subscribe to additional topics don't error.
func (b *Bus) Subscribe(_ context.Context, subject string, _ ...client.SubscribeOption) (<-chan client.Message, error) {
	if b.isClosed() {
		return nil, client.ErrClosed
	}
	ch := make(chan client.Message, 64)
	envs, err := b.envelopesFor(subject)
	if err != nil {
		close(ch)
		return ch, nil
	}
	for _, env := range envs {
		ch <- client.Message{
			Envelope:  env,
			Subject:   subject,
			Timestamp: env.Ts.Time,
			Ack:       noopAck,
		}
	}
	b.mu.Lock()
	b.channels = append(b.channels, ch)
	b.mu.Unlock()
	return ch, nil
}

// envelopesFor builds the canned envelope slice for subject. Returns
// (nil, err) when the subject is unsupported; callers translate that
// into "open but idle".
func (b *Bus) envelopesFor(subject string) ([]sextantproto.Envelope, error) {
	if subject == "user_input.>" {
		return b.pendingEnvelopes(), nil
	}
	// agents.<uuid>.frames | agents.<uuid>.lifecycle
	id, suffix, ok := parseAgentSubject(subject)
	if !ok {
		return nil, fmt.Errorf("unknown subject %q", subject)
	}
	switch suffix {
	case "frames":
		frames := b.fx.Conversations[id]
		return framesToEnvelopes(id, frames), nil
	case "lifecycle":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown subject suffix %q", suffix)
	}
}

func parseAgentSubject(subject string) (uuid.UUID, string, bool) {
	// Expected shape: agents.<uuid>.<suffix>.
	const prefix = "agents."
	if len(subject) <= len(prefix) || subject[:len(prefix)] != prefix {
		return uuid.Nil, "", false
	}
	rest := subject[len(prefix):]
	// uuid is 36 chars + '.'
	if len(rest) < 37 || rest[36] != '.' {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(rest[:36])
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, rest[37:], true
}

// pendingEnvelopes wraps every fixture.Pending request in a
// user_input_request envelope.
func (b *Bus) pendingEnvelopes() []sextantproto.Envelope {
	envs := make([]sextantproto.Envelope, 0, len(b.fx.Pending))
	for _, p := range b.fx.Pending {
		raw, err := json.Marshal(p)
		if err != nil {
			continue
		}
		from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: p.FromUUID.String()}
		env := sextantproto.NewEnvelope(sextantproto.KindUserInputRequest, from, raw)
		envs = append(envs, env)
	}
	return envs
}

// framesToEnvelopes wraps every fixture.Frame in an agent_frame envelope
// keyed to agentID. The envelope timestamp is the frame Ts so the chat
// renderer's "time since" math works against fixture data.
func framesToEnvelopes(agentID uuid.UUID, frames []Frame) []sextantproto.Envelope {
	envs := make([]sextantproto.Envelope, 0, len(frames))
	for _, f := range frames {
		body := f.Body
		if body == nil {
			body = map[string]any{}
		}
		if f.Text != "" && body["text"] == nil {
			body["text"] = f.Text
		}
		kind := f.FrameKind
		if kind == "" && f.Actor == ActorUser {
			// User frames in the fixture are operator local-echo: they have
			// no on-the-wire FrameKind. We synthesize a system_note kind
			// here so the envelope validates, but the chat-side adapter
			// reads f.Actor / f.Text directly without going through
			// envelopes for user turns.
			kind = sextantproto.FrameSystemNote
		}
		payload := sextantproto.AgentFramePayload{
			FrameKind: kind,
			ToolName:  f.ToolName,
			Body:      body,
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
		env := sextantproto.NewEnvelope(sextantproto.KindAgentFrame, from, raw)
		// Override the auto-assigned wall-clock time so the fixture's
		// frames render with their canned timestamps.
		if !f.Ts.IsZero() {
			env.Ts = sextantproto.AtTimestamp(f.Ts)
		}
		envs = append(envs, env)
	}
	return envs
}

// WatchKV returns a channel that emits one update reflecting the
// current value at bucket/key and then idles. The fake never has
// background writers so subsequent updates only land via PutKV from
// the same process.
func (b *Bus) WatchKV(_ context.Context, bucket, key string) (<-chan client.KVUpdate, error) {
	if b.isClosed() {
		return nil, client.ErrClosed
	}
	ch := make(chan client.KVUpdate, 4)
	b.mu.Lock()
	if v, ok := b.kvState[bucket][key]; ok {
		ch <- client.KVUpdate{
			Bucket:    bucket,
			Key:       key,
			Value:     v,
			Op:        client.KVOpPut,
			Timestamp: time.Now(),
		}
	}
	b.kvWatch = append(b.kvWatch, ch)
	b.mu.Unlock()
	return ch, nil
}

// PutKV stores value at bucket/key and broadcasts the put to every
// active watcher. The fixture-bus is single-process so broadcasting is
// a tight loop, not a fan-out.
func (b *Bus) PutKV(_ context.Context, bucket, key string, value []byte) error {
	if b.isClosed() {
		return client.ErrClosed
	}
	b.mu.Lock()
	if b.kvState[bucket] == nil {
		b.kvState[bucket] = map[string][]byte{}
	}
	b.kvState[bucket][key] = append([]byte(nil), value...)
	upd := client.KVUpdate{
		Bucket:    bucket,
		Key:       key,
		Value:     append([]byte(nil), value...),
		Op:        client.KVOpPut,
		Timestamp: time.Now(),
	}
	watchers := append([]chan client.KVUpdate(nil), b.kvWatch...)
	b.mu.Unlock()
	for _, w := range watchers {
		select {
		case w <- upd:
		default:
		}
	}
	return nil
}

// GetKV reads a previously-put value. Returns ErrKVKeyNotFound when
// nothing's been written.
func (b *Bus) GetKV(_ context.Context, bucket, key string) ([]byte, error) {
	if b.isClosed() {
		return nil, client.ErrClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.kvState[bucket][key]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, client.ErrKVKeyNotFound
}

func (b *Bus) isClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

func noopAck() error { return nil }

func filterAgents(in []sextantproto.AgentSummary, filter *sextantproto.ListAgentsFilter) []sextantproto.AgentSummary {
	if filter == nil || filter.Lifecycle == "" {
		out := make([]sextantproto.AgentSummary, len(in))
		copy(out, in)
		return out
	}
	out := make([]sextantproto.AgentSummary, 0, len(in))
	for _, a := range in {
		if a.Lifecycle == filter.Lifecycle {
			out = append(out, a)
		}
	}
	return out
}
