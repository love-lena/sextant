package main

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/clients/go/apps/mcp/attest"
)

// TestDecideFire covers the status hook's two gates (TASK-87): skip-if-not-connected
// (no per-session identity file), then throttle (Haiku at most once per interval).
// Bus-free: it seeds the identity + throttle-state files the real hook reads.
func TestDecideFire(t *testing.T) {
	dir := t.TempDir()
	sess := "sess-1"
	now := time.Unix(2_000_000, 0)
	interval := 45 * time.Second

	// 1. Not connected: no identity file ⇒ skip (a regular non-bus session).
	if fire, reason := decideFire(dir, sess, sess, now, interval); fire {
		t.Fatalf("want skip when not connected, got fire (%s)", reason)
	}

	// Connect: seed the identity file the MCP server would write on connect.
	if err := attest.SaveIdentity(dir, sess, attest.Identity{Creds: "/tmp/x.creds", ID: "01TESTWORKER"}); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}

	// 2. Connected + fresh (never fired) ⇒ fire, and the throttle state advances.
	if fire, reason := decideFire(dir, sess, sess, now, interval); !fire {
		t.Fatalf("want fire when connected + fresh, got skip (%s)", reason)
	}

	// 3. Within the interval ⇒ throttled (the previous fire advanced the state).
	if fire, _ := decideFire(dir, sess, sess, now.Add(10*time.Second), interval); fire {
		t.Fatalf("want throttled 10s after a fire")
	}

	// 4. After the interval ⇒ fire again.
	if fire, reason := decideFire(dir, sess, sess, now.Add(60*time.Second), interval); !fire {
		t.Fatalf("want fire after the interval elapsed, got skip (%s)", reason)
	}

	// No plugin-data dir ⇒ skip (can't gate or throttle).
	if fire, _ := decideFire("", sess, sess, now, interval); fire {
		t.Fatalf("want skip with no CLAUDE_PLUGIN_DATA")
	}
}
