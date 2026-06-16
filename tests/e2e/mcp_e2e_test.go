//go:build e2e

// The sextant-mcp acceptance (TASK-22): drives the built MCP server over
// stdio JSON-RPC against a real bus — handshake + every tool under one
// verified identity (AC#1), channel delivery with reply round-trip (AC#2),
// held-connection presence (AC#6), and mid-session identity heal with
// actionable pre-connection errors (AC#7). The lost-tail notices (AC#8) are
// covered at the unit layer (channel_test.go: TestResumeNoticeDeferredVsLost);
// simulating a wiped store mid-e2e has no harness precedent yet.
package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const mcpTopic = "msg.topic.e2e"

func TestMCPAcceptance(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	// alice: the CLI peer, operator-minted, creds in the bus store.
	aliceOut, code := h.run(nil, "clients", "register", "alice", "--store", h.store)
	if code != 0 {
		t.Fatalf("register alice exited %d: %s", code, aliceOut)
	}
	aliceCreds := h.store + "/alice.creds"

	// claude-agent: the MCP server's identity, self-enrolled into its own
	// context store (one context per agent).
	agentHome := t.TempDir()
	agentOut, code := h.run(map[string]string{"SEXTANT_HOME": agentHome, "USER": "claude-agent"},
		"clients", "register", "--self", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self exited %d: %s", code, agentOut)
	}
	agentID := mustParseID(t, agentOut, `enrolled as (`+ulidPat+`)`)

	// The MCP server no longer inherits the active context (ADR-0029), so the
	// agent identity is pinned explicitly — exercising the $SEXTANT_CONTEXT path.
	srv := startMCP(t, h, mcpBin, map[string]string{
		"SEXTANT_HOME":    agentHome,
		"SEXTANT_STORE":   h.store,
		"SEXTANT_CONTEXT": "claude-agent",
	})

	// --- handshake: capability + tool surface (AC#1) -------------------------
	initRes := srv.call(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
	})
	if !strings.Contains(string(initRes), "claude/channel") {
		t.Fatalf("initialize result missing the channel capability: %s", initRes)
	}
	srv.notify(t, "notifications/initialized", map[string]any{})

	toolsRes := srv.call(t, "tools/list", map[string]any{})
	var tl struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsRes, &tl); err != nil {
		t.Fatalf("parse tools/list: %v", err)
	}
	want := map[string]bool{
		"message_publish": true, "message_read": true, "message_subscribe": true,
		"message_unsubscribe": true, "artifact_create": true, "artifact_update": true,
		"artifact_get": true, "artifact_list": true, "artifact_delete": true, "clients_list": true,
		"context_use": true,
	}
	if len(tl.Tools) != len(want) {
		t.Fatalf("tools/list returned %d tools, want %d: %s", len(tl.Tools), len(want), toolsRes)
	}
	for _, tool := range tl.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
	}

	// --- every one-shot + pull-batch tool against the live bus (AC#1) --------
	if out := srv.tool(t, "artifact_create", `{"name":"plan","record":{"$type":"document","title":"T","body":"v1"}}`); !strings.Contains(out, `"revision":1`) {
		t.Fatalf("artifact_create: %s", out)
	}
	if out := srv.tool(t, "artifact_update", `{"name":"plan","record":{"$type":"document","title":"T","body":"v2"},"expected_rev":1}`); !strings.Contains(out, `"revision":2`) {
		t.Fatalf("artifact_update: %s", out)
	}
	if out := srv.tool(t, "artifact_get", `{"name":"plan"}`); !strings.Contains(out, "v2") {
		t.Fatalf("artifact_get: %s", out)
	}
	if out := srv.tool(t, "artifact_list", `{}`); !strings.Contains(out, "plan") {
		t.Fatalf("artifact_list: %s", out)
	}
	if out := srv.tool(t, "clients_list", `{}`); !strings.Contains(out, "alice") || !strings.Contains(out, "claude-agent") {
		t.Fatalf("clients_list: %s", out)
	}
	if out := srv.tool(t, "message_publish", `{"subject":"`+mcpTopic+`","record":{"$type":"chat.message","text":"hello from mcp"}}`); !strings.Contains(out, "published") {
		t.Fatalf("message_publish: %s", out)
	}
	readOut := srv.tool(t, "message_read", `{"subject":"`+mcpTopic+`"}`)
	if !strings.Contains(readOut, "hello from mcp") || !strings.Contains(readOut, agentID) {
		t.Fatalf("message_read missing the published frame with the agent author: %s", readOut)
	}
	if !strings.Contains(readOut, `"author_display":"claude-agent"`) {
		t.Fatalf("message_read missing the resolved display name: %s", readOut)
	}
	if out := srv.tool(t, "artifact_delete", `{"name":"plan"}`); !strings.Contains(out, "deleted") {
		t.Fatalf("artifact_delete: %s", out)
	}

	// --- held connection: presence online between tool calls (AC#6) ----------
	h.waitPresence(t, aliceCreds, agentID, true)

	// --- channel: subscribed notice, delivery, reply round-trip (AC#2) -------
	subOut := srv.tool(t, "message_subscribe", `{"subject":"`+mcpTopic+`"}`)
	if !strings.Contains(subOut, "dangerously-load-development-channels") {
		t.Fatalf("subscribe result missing the delivery caveat: %s", subOut)
	}
	notice := srv.waitEvent(t, func(ev channelEvent) bool { return ev.meta("event") == "subscribed" })
	if notice.meta("subject") != mcpTopic {
		t.Fatalf("subscribed notice subject = %q", notice.meta("subject"))
	}

	if out, code := h.run(nil, "publish", mcpTopic, `{"$type":"chat.message","text":"ping from alice"}`, "--creds", aliceCreds, "--store", h.store); code != 0 {
		t.Fatalf("alice publish exited %d: %s", code, out)
	}
	ev := srv.waitEvent(t, func(ev channelEvent) bool { return ev.Content == "ping from alice" })
	if ev.meta("sender") != "alice" || ev.meta("subject") != mcpTopic {
		t.Fatalf("channel event meta = %+v", ev.Meta)
	}
	if ev.meta("seq") == "" || ev.meta("id") == "" {
		t.Fatalf("channel event missing seq/id: %+v", ev.Meta)
	}

	if out := srv.tool(t, "message_publish", `{"subject":"`+mcpTopic+`","record":{"$type":"chat.message","text":"pong from claude"}}`); !strings.Contains(out, "published") {
		t.Fatalf("reply publish: %s", out)
	}
	deadline := time.Now().Add(stepTimeout)
	for {
		out, code := h.run(nil, "read", mcpTopic, "--creds", aliceCreds, "--store", h.store)
		if code == 0 && strings.Contains(out, "pong from claude") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alice never saw the reply: %s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// --- unsubscribe stops delivery -------------------------------------------
	if out := srv.tool(t, "message_unsubscribe", `{"subject":"`+mcpTopic+`"}`); strings.Contains(out, mcpTopic) {
		t.Fatalf("unsubscribe should leave no active subscriptions: %s", out)
	}
}

// TestMCPAutoMintAndResume is AC#8/#9 + AC#2/#3 (ADR-0029): with nothing
// pinned, the server provisions its OWN identity on first use — without
// touching the operator's active context — keyed by CLAUDE_CODE_SESSION_ID so a
// resumed session (same id) reattaches to it, while a different id is a
// different identity.
func TestMCPAutoMintAndResume(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	// An operator owns the active context in the agent's home. The MCP server
	// must neither speak as it nor disturb it.
	home := t.TempDir()
	if out, code := h.run(map[string]string{"SEXTANT_HOME": home, "USER": "operator"},
		"clients", "register", "--self", "--store", h.store); code != 0 {
		t.Fatalf("register operator: %s", out)
	}
	if got := readActive(t, home); got != "operator" {
		t.Fatalf("precondition: active = %q, want operator", got)
	}

	start := func(sessionID string) *mcpProc {
		srv := startMCP(t, h, mcpBin, map[string]string{
			"SEXTANT_HOME": home, "SEXTANT_STORE": h.store,
			"CLAUDE_CODE_SESSION_ID": sessionID,
		})
		initMCP(t, srv)
		return srv
	}

	// First session: auto-mints (no register --self), and the operator's active
	// context is untouched.
	id1 := start("sess-A").selfID(t, "msg.topic.probe-a1")
	if got := readActive(t, home); got != "operator" {
		t.Fatalf("active changed to %q — auto-mint must not disturb the operator", got)
	}

	// Resume: same session id reattaches to the same identity.
	if id2 := start("sess-A").selfID(t, "msg.topic.probe-a2"); id2 != id1 {
		t.Fatalf("resume minted a new identity %s, want reattach to %s", id2, id1)
	}

	// A different session is a different identity (no dup-answer).
	if id3 := start("sess-B").selfID(t, "msg.topic.probe-b"); id3 == id1 {
		t.Fatalf("a different session shares the identity %s — sessions must be distinct", id3)
	}
}

// TestMCPRestoresSubscriptionsAfterResume is the TASK-124 acceptance: a fresh
// MCP process (a resume — same session id + plugin-data, a brand-new process
// with an empty in-memory sub map) automatically re-establishes the manual
// subscription the prior process held AND catches up the frames published while
// it was gone — without the agent calling message_subscribe again. This is the
// durable self-healing the in-memory-only sub map lacked (modes A/B).
func TestMCPRestoresSubscriptionsAfterResume(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	home := t.TempDir()
	pluginData := t.TempDir() // CLAUDE_PLUGIN_DATA — the substate persists here across the resume
	const subj = "msg.topic.t124-restore"

	start := func(sessionID string) *mcpProc {
		srv := startMCP(t, h, mcpBin, map[string]string{
			"SEXTANT_HOME":           home,
			"SEXTANT_STORE":          h.store,
			"CLAUDE_CODE_SESSION_ID": sessionID,
			"CLAUDE_PLUGIN_DATA":     pluginData,
		})
		initMCP(t, srv)
		return srv
	}

	// A peer with a distinct identity publishes the missed frames — a different
	// author, so the restored session never self-echo-suppresses them.
	peer := start("sess-peer")
	_ = peer.selfID(t, "msg.topic.t124-peer-probe") // force connect/mint
	pub := func(text string) {
		if out := peer.tool(t, "message_publish", `{"subject":"`+subj+`","record":{"$type":"chat.message","text":"`+text+`"}}`); !strings.Contains(out, "published") {
			t.Fatalf("peer publish %q: %s", text, out)
		}
	}

	// Process 1: subscribe to subj, then receive one live frame so the cursor is
	// primed (it advances past the start of history — the real bug is an active
	// subscription that has been delivering, then resumes).
	p1 := start("sess-124")
	if out := p1.tool(t, "message_subscribe", `{"subject":"`+subj+`"}`); !strings.Contains(out, subj) {
		t.Fatalf("subscribe: %s", out)
	}
	pub("primer")
	p1.waitEvent(t, func(ev channelEvent) bool { return ev.Content == "primer" })
	time.Sleep(200 * time.Millisecond) // let the cursor advance land before the flush

	// Graceful resume: stdin EOF makes p1 flush its (primed) cursor on shutdown.
	p1.stop()

	// Dead window: the peer publishes frames p1's now-dead subscription never saw.
	pub("missed-1")
	pub("missed-2")

	// Process 2 (resume): same session id + plugin-data, a fresh process with an
	// empty sub map. Its first tool call connects, and onConnect restores subj +
	// catches up from the primed cursor — the agent itself never calls
	// message_subscribe.
	p2 := start("sess-124")
	_ = p2.selfID(t, "msg.topic.t124-p2-probe") // trigger the lazy connect → restore

	// The catch-up must deliver the two dead-window frames and must NOT re-deliver
	// the already-seen primer (the cursor stored the NEXT seq to read, not the
	// last-delivered one). Catch-up replays in seq order, so a re-read primer
	// would arrive first.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev := p2.waitEvent(t, func(ev channelEvent) bool {
			return ev.Content == "primer" || ev.Content == "missed-1" || ev.Content == "missed-2"
		})
		if ev.Content == "primer" {
			t.Fatalf("restore re-delivered the already-seen primer — cursor off-by-one")
		}
		got[ev.Content] = true
	}
	if !got["missed-1"] || !got["missed-2"] {
		t.Fatalf("resume did not catch up the missed frames: got %v", got)
	}
}

// TestMCPNoBusActionableError is AC#7: when the server cannot mint (no bus, no
// enrollment credential) and nothing is pinned, it returns the recovery recipe
// rather than borrowing the operator's identity.
func TestMCPNoBusActionableError(t *testing.T) {
	h := newHarness(t)
	mcpBin := buildMCPBinary(t) // no startBus; empty store below
	srv := startMCP(t, h, mcpBin, map[string]string{
		"SEXTANT_HOME": t.TempDir(), "SEXTANT_STORE": t.TempDir(),
		"CLAUDE_CODE_SESSION_ID": "sess-nobus",
	})
	initMCP(t, srv)
	out := srv.tool(t, "clients_list", `{}`)
	for _, want := range []string{"agent identity", "$SEXTANT_CONTEXT", "enroll.creds"} {
		if !strings.Contains(out, want) {
			t.Errorf("no-bus error %q missing %q", out, want)
		}
	}
}

// --- the stdio JSON-RPC driver ----------------------------------------------

type channelEvent struct {
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta"`
}

func (e channelEvent) meta(k string) string {
	v, _ := e.Meta[k].(string)
	return v
}

type mcpProc struct {
	enc      *json.Encoder
	mu       sync.Mutex
	nextID   atomic.Int64
	resps    map[int64]chan json.RawMessage
	events   chan channelEvent
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	shutOnce sync.Once
}

// stop gracefully shuts the server down and reaps it — a resume simulation.
// Closing stdin gives the stdio server EOF so it returns from Run and flushes
// the durable substate (TASK-124) on the way out, so the next process restores
// from a fully-persisted cursor. Falls back to a kill if it doesn't exit
// promptly. Idempotent (the t.Cleanup also calls it), so a test may stop a
// process mid-run.
func (p *mcpProc) stop() {
	p.shutOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.cmd == nil || p.cmd.Process == nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = p.cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	})
}

func startMCP(t *testing.T, h *harness, bin string, env map[string]string) *mcpProc {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = h.childEnv(env) // extra env wins, incl. the per-agent SEXTANT_HOME
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = nil // the server logs to stderr; keep the test output clean
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sextant-mcp: %v", err)
	}

	p := &mcpProc{
		enc:    json.NewEncoder(stdin),
		resps:  map[int64]chan json.RawMessage{},
		events: make(chan channelEvent, 64),
		cmd:    cmd,
		stdin:  stdin,
	}
	t.Cleanup(p.stop)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for sc.Scan() {
			var msg struct {
				ID     *int64          `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				continue
			}
			switch {
			case msg.ID != nil && msg.Method == "":
				p.mu.Lock()
				ch := p.resps[*msg.ID]
				p.mu.Unlock()
				if ch != nil {
					if msg.Error != nil {
						ch <- msg.Error
					} else {
						ch <- msg.Result
					}
				}
			case msg.Method == "notifications/claude/channel":
				var ev channelEvent
				if err := json.Unmarshal(msg.Params, &ev); err == nil {
					p.events <- ev
				}
			}
		}
	}()
	return p
}

func (p *mcpProc) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	id := p.nextID.Add(1)
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.resps[id] = ch
	p.mu.Unlock()
	if err := p.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	select {
	case res := <-ch:
		return res
	case <-time.After(stepTimeout):
		t.Fatalf("%s: no response within %s", method, stepTimeout)
		return nil
	}
}

func (p *mcpProc) notify(t *testing.T, method string, params any) {
	t.Helper()
	if err := p.enc.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
}

// tool calls tools/call and returns the result's first text content.
func (p *mcpProc) tool(t *testing.T, name, argsJSON string) string {
	t.Helper()
	res := p.call(t, "tools/call", map[string]any{
		"name":      name,
		"arguments": json.RawMessage(argsJSON),
	})
	var out struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(res, &out); err != nil || len(out.Content) == 0 {
		t.Fatalf("tool %s returned unparseable result: %s", name, res)
	}
	return out.Content[0].Text
}

func (p *mcpProc) waitEvent(t *testing.T, match func(channelEvent) bool) channelEvent {
	t.Helper()
	deadline := time.After(stepTimeout)
	for {
		select {
		case ev := <-p.events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("no matching channel event within %s", stepTimeout)
		}
	}
}

// initMCP runs the MCP lifecycle handshake.
func initMCP(t *testing.T, srv *mcpProc) {
	t.Helper()
	srv.call(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
	})
	srv.notify(t, "notifications/initialized", map[string]any{})
}

// selfID publishes a probe to a fresh subject and reads it back, returning the
// agent's own bus-stamped author ULID — the identity it actually connected as.
func (p *mcpProc) selfID(t *testing.T, subject string) string {
	t.Helper()
	if out := p.tool(t, "message_publish", `{"subject":"`+subject+`","record":{"$type":"chat.message","text":"id-probe"}}`); !strings.Contains(out, "published") {
		t.Fatalf("probe publish: %s", out)
	}
	out := p.tool(t, "message_read", `{"subject":"`+subject+`"}`)
	var res struct {
		Messages []struct {
			Author string `json:"author"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Messages) == 0 {
		t.Fatalf("selfID: unparseable read or no messages: %s", out)
	}
	return res.Messages[len(res.Messages)-1].Author
}

// readActive returns the active context name recorded under a SEXTANT_HOME.
func readActive(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "active"))
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func buildMCPBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/sextant-mcp"
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/sextant-mcp")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sextant-mcp: %v\n%s", err, out)
	}
	return bin
}
