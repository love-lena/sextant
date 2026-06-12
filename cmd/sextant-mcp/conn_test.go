package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/clictx"
)

func cf(creds, store, url, ctxName string) connFlags {
	return connFlags{creds: &creds, store: &store, url: &url, context: &ctxName}
}

// resolved builds a ResolvedConn for provenance tests.
func resolved(url, ctxName string) clictx.ResolvedConn {
	return clictx.ResolvedConn{URL: url, Context: ctxName}
}

// TestGetNoBusNamesAgentMintRecipe: with nothing pinned and no reachable bus,
// the server cannot mint its own identity — and must NOT borrow the operator's
// (ADR-0029). The error is the recovery recipe (AC#7).
func TestGetNoBusNamesAgentMintRecipe(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	m := &connManager{cf: cf("", t.TempDir(), "", "")}
	_, err := m.get(context.Background())
	if err == nil {
		t.Fatal("get() succeeded with no identity and no bus")
	}
	for _, want := range []string{"agent identity", "$SEXTANT_CONTEXT", "$SEXTANT_CREDS", "enroll.creds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestGetConnectFailureNamesURLProvenance(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Explicit creds path that doesn't exist + explicit URL: fails fast, and
	// the error must carry the URL and its source (ADR-0025 provenance).
	m := &connManager{cf: cf("/nonexistent/agent.creds", t.TempDir(), "nats://127.0.0.1:1", "")}
	_, err := m.get(ctx)
	if err == nil {
		t.Fatal("get() succeeded against a closed port")
	}
	for _, want := range []string{"nats://127.0.0.1:1", "--url", "/nonexistent/agent.creds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestURLProvenance(t *testing.T) {
	store := t.TempDir()
	cases := []struct {
		name string
		m    *connManager
		rcURL, rcCtx,
		want string
	}{
		{"flag", &connManager{cf: cf("", store, "nats://flag:1", "")}, "nats://flag:1", "", "--url"},
		{"context", &connManager{cf: cf("", store, "", "alpha")}, "nats://ctx:1", "alpha", `context "alpha"`},
		{"discovery", &connManager{cf: cf("/x.creds", store, "", "")}, "", "", "bus.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.m.urlProvenance(resolved(tc.rcURL, tc.rcCtx))
			if !strings.Contains(got, tc.want) {
				t.Errorf("urlProvenance() = %q, missing %q", got, tc.want)
			}
		})
	}
}
