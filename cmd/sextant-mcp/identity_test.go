package main

import (
	"context"
	"strings"
	"testing"

	"github.com/love-lena/sextant/internal/clictx"
)

// failMint fails the test if the agent-mint path is taken — used to prove a
// branch resolves an existing identity without provisioning a new one.
func failMint(t *testing.T) func(context.Context, string, string) (clictx.ResolvedConn, error) {
	return func(context.Context, string, string) (clictx.ResolvedConn, error) {
		t.Helper()
		t.Fatal("mint must not be called on this path")
		return clictx.ResolvedConn{}, nil
	}
}

// TestResolveExplicitCredsSkipsMint: explicit $SEXTANT_CREDS/--creds resolve as
// before — never the mint path, never the active context.
func TestResolveExplicitCredsSkipsMint(t *testing.T) {
	m := &connManager{cf: cf("/some/agent.creds", t.TempDir(), "nats://u", "")}
	m.mint = failMint(t)
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Creds != "/some/agent.creds" {
		t.Fatalf("creds = %q, want the explicit creds", rc.Creds)
	}
}

// TestResolveExplicitContextSkipsMint: a pinned $SEXTANT_CONTEXT/--context
// resolves to that context, never the mint path.
func TestResolveExplicitContextSkipsMint(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "pinned", URL: "nats://c", ID: "01P", Creds: "/p.creds"}); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "pinned")}
	m.mint = failMint(t)
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "pinned" || rc.Creds != "/p.creds" {
		t.Fatalf("resolved %+v, want the pinned context", rc)
	}
}

// TestResolveNoPinsMintsAndIgnoresActive is the core of ADR-0029: with nothing
// pinned, the MCP server provisions its OWN identity and must NEVER inherit the
// operator's active context — and must not disturb it.
func TestResolveNoPinsMintsAndIgnoresActive(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "operator", URL: "u", ID: "01OP", Creds: "/op.creds"}); err != nil {
		t.Fatal(err)
	}
	if err := clictx.SetActive("operator"); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	called := false
	m.mint = func(_ context.Context, name, _ string) (clictx.ResolvedConn, error) {
		called = true
		return clictx.ResolvedConn{Creds: "/agent.creds", Context: name}, nil
	}
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("mint not called — resolve must provision its own identity, not inherit one")
	}
	if rc.Creds == "/op.creds" || rc.Context == "operator" {
		t.Fatalf("resolve inherited the operator's active context: %+v", rc)
	}
	if clictx.Active() != "operator" {
		t.Fatalf("Active() = %q — resolve must not disturb the operator's active context", clictx.Active())
	}
}

// TestResolveReattachesSessionContext: a context already minted for this
// CLAUDE_CODE_SESSION_ID is reattached (resume), not minted afresh.
func TestResolveReattachesSessionContext(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "sess-123")
	name, _, err := agentContextName()
	if err != nil {
		t.Fatal(err)
	}
	if err := clictx.Save(clictx.Context{Name: name, URL: "nats://s", ID: "01SESS", Creds: "/s.creds"}); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	m.mint = failMint(t)
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != name || rc.Creds != "/s.creds" {
		t.Fatalf("resolved %+v, want reattach to %q", rc, name)
	}
}

// TestUseSwitchesIdentity: context_use attaches this session to an existing
// agent context, and resolve then connects as it.
func TestUseSwitchesIdentity(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "agent-b", URL: "nats://b", ID: "01B", Kind: "agent", Creds: "/b.creds"}); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	m.mint = failMint(t)
	if err := m.use("agent-b"); err != nil {
		t.Fatalf("use: %v", err)
	}
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "agent-b" {
		t.Fatalf("after use, resolved %+v, want agent-b", rc)
	}
}

// TestUseRefusesNonAgent: context_use must refuse any non-agent identity — not
// just kind "human" (the dash's label) but also "client", which is what
// `register --self` mints for an operator. Otherwise the agent could switch
// into the operator's own identity and speak as them — the very impersonation
// ADR-0029 forbids.
func TestUseRefusesNonAgent(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	for _, tc := range []struct{ name, kind string }{
		{"lena-dash", "human"}, // dash-minted human
		{"lena-cli", "client"}, // `register --self` default — an operator
		{"unlabelled", ""},     // `context add` default
	} {
		if err := clictx.Save(clictx.Context{Name: tc.name, URL: "u", ID: "01" + tc.name, Kind: tc.kind, Creds: "/x.creds"}); err != nil {
			t.Fatal(err)
		}
		m := &connManager{cf: cf("", t.TempDir(), "", "")}
		if err := m.use(tc.name); err == nil {
			t.Fatalf("use(%q) kind=%q succeeded — only agent identities may be switched to", tc.name, tc.kind)
		}
		if m.switched != "" {
			t.Fatalf("switched set to %q despite refusal", m.switched)
		}
	}
}

// TestUseUnknownListsAvailable: switching to a missing context names what is
// available, so Claude can recover.
func TestUseUnknownListsAvailable(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "agent-a", URL: "u", ID: "01A", Kind: "agent", Creds: "/a.creds"}); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	err := m.use("nope")
	if err == nil {
		t.Fatal("use() of an unknown context should fail")
	}
	if !strings.Contains(err.Error(), "agent-a") {
		t.Fatalf("error %q should list available contexts", err)
	}
}

// TestResolveSwitchedContextSkipsMint: once Claude has explicitly switched
// (context_use), that context wins over minting a fresh one.
func TestResolveSwitchedContextSkipsMint(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "other-agent", URL: "nats://o", ID: "01OTH", Creds: "/o.creds"}); err != nil {
		t.Fatal(err)
	}
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	m.switched = "other-agent"
	m.mint = failMint(t)
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "other-agent" || rc.Creds != "/o.creds" {
		t.Fatalf("resolved %+v, want the switched context", rc)
	}
}

// TestAgentContextName: the context handle is keyed by CLAUDE_CODE_SESSION_ID so
// a resumed session reattaches; absent the session id it is unique-per-process
// and non-persistent (no resume key).
func TestAgentContextName(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc-def")
	n1, p1, err := agentContextName()
	if err != nil {
		t.Fatal(err)
	}
	n2, p2, _ := agentContextName()
	if !p1 || !p2 {
		t.Fatal("a present session id must be persistent (reattachable)")
	}
	if n1 != n2 {
		t.Fatalf("same session id gave different handles: %q vs %q", n1, n2)
	}
	if !strings.Contains(n1, "abc-def") {
		t.Fatalf("handle %q does not encode the session id", n1)
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "xyz-999")
	if n3, _, _ := agentContextName(); n3 == n1 {
		t.Fatal("different session ids must give different handles")
	}

	// A session id that can't be a context handle falls back to a fresh,
	// non-persistent identity instead of producing an unusable handle.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "bad/id")
	if _, persistent, err := agentContextName(); err != nil || persistent {
		t.Fatalf("an unusable session id should fall back to non-persistent: persistent=%v err=%v", persistent, err)
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	n4, p4, _ := agentContextName()
	n5, p5, _ := agentContextName()
	if p4 || p5 {
		t.Fatal("no session id must be non-persistent")
	}
	if n4 == n5 {
		t.Fatalf("no session id must mint a unique handle each process: %q == %q", n4, n5)
	}
}
