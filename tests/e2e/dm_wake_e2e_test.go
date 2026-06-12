//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
)

// TestMCPDMWakesWithoutSubscribe is the review-M1 definition-of-done: a frame
// published to the worker's OWN DM subject (msg.client.<self>) produces a
// channel wake event through the MCP server WITHOUT any explicit
// message_subscribe call. This is the auto-DM bridge (TASK-55 c.DMs() drained
// into frameEvent) wired in cmd/sextant-mcp; before it, a principal DM landed in
// the durable stream but nothing woke the worker.
//
// It proves: (1) a peer's DM wakes via a channel event with the body (CONTENT
// mode); (2) the worker's OWN published DM is suppressed (self-echo) — no wake.
func TestMCPDMWakesWithoutSubscribe(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	// alice: the CLI peer who will DM the agent.
	aliceOut, code := h.run(nil, "clients", "register", "alice", "--store", h.store)
	if code != 0 {
		t.Fatalf("register alice exited %d: %s", code, aliceOut)
	}
	aliceID := mustParseID(t, aliceOut, `registered alice as (`+ulidPat+`)`)
	aliceCreds := h.store + "/alice.creds"

	// The agent: the MCP server's identity, self-enrolled into its own home.
	agentHome := t.TempDir()
	agentOut, code := h.run(map[string]string{"SEXTANT_HOME": agentHome, "USER": "claude-agent"},
		"clients", "register", "--self", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self exited %d: %s", code, agentOut)
	}
	agentID := mustParseID(t, agentOut, `enrolled as (`+ulidPat+`)`)
	agentDM := sx.ClientSubject(agentID)

	srv := startMCP(t, h, mcpBin, map[string]string{
		"SEXTANT_HOME":  agentHome,
		"SEXTANT_STORE": h.store,
	})
	srv.call(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
	})
	srv.notify(t, "notifications/initialized", map[string]any{})

	// One tool call to trigger the lazy connect — the DM drain starts here.
	// No message_subscribe is ever called; the auto-DM bridge is the only path.
	if out := srv.tool(t, "clients_list", `{}`); !strings.Contains(out, "claude-agent") {
		t.Fatalf("clients_list (connect trigger): %s", out)
	}

	// alice DMs the agent's OWN subject. With the bridge, this wakes the session.
	if out, code := h.run(nil, "publish", agentDM, `{"$type":"chat.message","text":"wake up worker"}`,
		"--creds", aliceCreds, "--store", h.store); code != 0 {
		t.Fatalf("alice DM publish exited %d: %s", code, out)
	}

	ev := srv.waitEvent(t, func(ev channelEvent) bool { return ev.Content == "wake up worker" })
	if ev.meta("subject") != agentDM {
		t.Fatalf("DM wake event subject = %q, want %q", ev.meta("subject"), agentDM)
	}
	// sender_id is the unforgeable author and must equal alice's ULID. sender is
	// the resolved display name, which is cached-only on the delivery path: it is
	// "alice" once the name cache is warm, but may be the raw id on a cold first
	// frame (the documented frameEvent contract). Accept either for sender.
	if ev.meta("sender_id") != aliceID {
		t.Fatalf("DM wake event sender_id = %q, want alice %q", ev.meta("sender_id"), aliceID)
	}
	if s := ev.meta("sender"); s != "alice" && s != aliceID {
		t.Fatalf("DM wake event sender = %q, want \"alice\" or the raw id %q", s, aliceID)
	}

	// The agent's OWN DM to itself must be suppressed (self-echo): publish via the
	// MCP tool (which records the frame id in the echo set) and assert no wake.
	if out := srv.tool(t, "message_publish", `{"subject":"`+agentDM+`","record":{"$type":"chat.message","text":"note to self"}}`); !strings.Contains(out, "published") {
		t.Fatalf("self DM publish: %s", out)
	}
	assertNoEvent(t, srv, func(ev channelEvent) bool { return ev.Content == "note to self" })
}

// assertNoEvent fails if a matching channel event arrives within a short window.
func assertNoEvent(t *testing.T, p *mcpProc, match func(channelEvent) bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-p.events:
			if match(ev) {
				t.Fatalf("a self-echo DM produced a channel event (should be suppressed): %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

// TestMCPSelfDMSubscribeNoDouble guards the fix for the double-delivery bug: the
// worker's own DM is already delivered by the auto-DM bridge, so an EXPLICIT
// message_subscribe to it must NOT open a second relay — otherwise every DM is
// pushed into the session twice. A DM must produce exactly ONE channel event
// even after the worker explicitly subscribes to its own DM.
func TestMCPSelfDMSubscribeNoDouble(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	aliceOut, code := h.run(nil, "clients", "register", "alice", "--store", h.store)
	if code != 0 {
		t.Fatalf("register alice exited %d: %s", code, aliceOut)
	}
	aliceCreds := h.store + "/alice.creds"

	agentHome := t.TempDir()
	agentOut, code := h.run(map[string]string{"SEXTANT_HOME": agentHome, "USER": "claude-agent"},
		"clients", "register", "--self", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self exited %d: %s", code, agentOut)
	}
	agentID := mustParseID(t, agentOut, `enrolled as (`+ulidPat+`)`)
	agentDM := sx.ClientSubject(agentID)

	srv := startMCP(t, h, mcpBin, map[string]string{"SEXTANT_HOME": agentHome, "SEXTANT_STORE": h.store})
	srv.call(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
	})
	srv.notify(t, "notifications/initialized", map[string]any{})

	// Connect (arms the auto-DM bridge), THEN explicitly subscribe the own DM —
	// the redundant case. It should report the DM active without a 2nd relay.
	if out := srv.tool(t, "clients_list", `{}`); !strings.Contains(out, "claude-agent") {
		t.Fatalf("clients_list (connect trigger): %s", out)
	}
	if out := srv.tool(t, "message_subscribe", `{"subject":"`+agentDM+`"}`); !strings.Contains(out, agentDM) {
		t.Fatalf("explicit self-DM subscribe should report it active: %s", out)
	}

	// alice DMs once.
	if out, code := h.run(nil, "publish", agentDM, `{"$type":"chat.message","text":"hello once"}`,
		"--creds", aliceCreds, "--store", h.store); code != 0 {
		t.Fatalf("alice DM publish exited %d: %s", code, out)
	}

	// Exactly one channel event: wait for the first, then assert no duplicate.
	_ = srv.waitEvent(t, func(ev channelEvent) bool { return ev.Content == "hello once" })
	assertNoEvent(t, srv, func(ev channelEvent) bool { return ev.Content == "hello once" })
}
