//go:build e2e

// TestSelfEchoSuppression is the TASK-52 definition-of-done: when the
// sextant-mcp server publishes to a subject it is subscribed to, the
// bus-relayed echo is NOT delivered as a channel event to the same session
// (AC#1), but another subscriber on the same subject DOES receive the frame
// (AC#2).
package e2e

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextant"
)

const selfEchoTopic = "msg.topic.self-echo-e2e"

func TestSelfEchoSuppression(t *testing.T) {
	h := newHarness(t)
	h.startBus()
	mcpBin := buildMCPBinary(t)

	// Register the MCP agent via self-enroll.
	agentHome := t.TempDir()
	agentOut, code := h.run(
		map[string]string{"SEXTANT_HOME": agentHome, "USER": "echo-agent"},
		"clients", "register", "--self", "--store", h.store,
	)
	if code != 0 {
		t.Fatalf("register --self exited %d: %s", code, agentOut)
	}

	// Register a peer client that will observe the frame from the outside.
	peerOut, code := h.run(nil, "clients", "register", "echo-peer", "--kind", "worker", "--store", h.store)
	if code != 0 {
		t.Fatalf("register echo-peer exited %d: %s", code, peerOut)
	}
	peerCreds := filepath.Join(h.store, "echo-peer.creds")

	bURL := busURL(t, h.store)

	// Connect the peer SDK client and subscribe to the topic.
	peer, err := sextant.Connect(context.Background(), sextant.Options{
		URL:       bURL,
		CredsPath: peerCreds,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("connect peer: %v", err)
	}
	defer peer.Close()

	peerDeliveries := make(chan sextant.Message, 8)
	_, err = peer.Subscribe(context.Background(), selfEchoTopic, func(m sextant.Message) {
		peerDeliveries <- m
	})
	if err != nil {
		t.Fatalf("peer Subscribe: %v", err)
	}

	// Start the MCP server, pinned to the self-enrolled identity (ADR-0029): the
	// server no longer inherits the active context, so pin $SEXTANT_CONTEXT to
	// connect as echo-agent rather than minting a throwaway per-session identity.
	srv := startMCP(t, h, mcpBin, map[string]string{
		"SEXTANT_HOME":    agentHome,
		"SEXTANT_STORE":   h.store,
		"SEXTANT_CONTEXT": "echo-agent",
	})
	srv.call(t, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0"},
	})
	srv.notify(t, "notifications/initialized", map[string]any{})

	// MCP agent subscribes to the topic.
	subOut := srv.tool(t, "message_subscribe", `{"subject":"`+selfEchoTopic+`"}`)
	if !strings.Contains(subOut, "dangerously-load-development-channels") {
		t.Fatalf("subscribe result missing delivery caveat: %s", subOut)
	}
	// Wait for the subscribed system notice to confirm subscription is live.
	srv.waitEvent(t, func(ev channelEvent) bool { return ev.meta("event") == "subscribed" })

	// MCP agent publishes to the subscribed topic.
	pubOut := srv.tool(t, "message_publish", `{"subject":"`+selfEchoTopic+`","record":{"$type":"chat.message","text":"self-echo-probe"}}`)
	if !strings.Contains(pubOut, "published") {
		t.Fatalf("message_publish: %s", pubOut)
	}

	// AC#2: the peer MUST receive the frame — suppression is self-only.
	select {
	case m := <-peerDeliveries:
		if m.Subject != selfEchoTopic {
			t.Errorf("peer delivery subject = %q, want %q", m.Subject, selfEchoTopic)
		}
		t.Logf("AC#2 pass: peer received frame %s on %s", m.Frame.ID, m.Subject)
	case <-time.After(stepTimeout):
		t.Fatal("AC#2 FAIL: peer never received the frame; suppression is not self-only")
	}

	// AC#1: the MCP agent must NOT receive the echo as a channel event.
	// We give the bus a small window to relay the echo (if suppression were
	// absent, the event would arrive quickly). stepTimeout / 10 is generous for
	// the relay path on a local bus.
	select {
	case ev := <-srv.events:
		// A system event (e.g. a second subscribed notice) is NOT a self-echo;
		// only a frame delivery from this topic is a violation.
		if ev.meta("event") == "" && ev.meta("subject") == selfEchoTopic {
			t.Errorf("AC#1 FAIL: self-echo delivered as channel event: content=%q meta=%v", ev.Content, ev.Meta)
		}
		// Any system events (unlikely but harmless) are fine; drain and continue.
	case <-time.After(stepTimeout / 10):
		t.Logf("AC#1 pass: no self-echo channel event within the relay window")
	}
}
