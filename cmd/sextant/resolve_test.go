package main

import (
	"errors"
	"testing"

	"github.com/love-lena/sextant/internal/clictx"
)

// cf builds connFlags from literal values (flag pointers) for resolution tests.
func cf(creds, store, url, context string) connFlags {
	return connFlags{creds: &creds, store: &store, url: &url, context: &context}
}

func TestResolveExplicitCredsWins(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	creds, url, err := cf("/x/a.creds", "/store", "nats://u", "").resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds != "/x/a.creds" || url != "nats://u" {
		t.Fatalf("resolve() = (%q,%q), want (/x/a.creds, nats://u)", creds, url)
	}
}

func TestResolveExplicitCredsIgnoresContext(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	mustSave(t, clictx.Context{Name: "alice", URL: "nats://ctx", Creds: "/ctx/a.creds"})
	mustSetActive(t, "alice")
	creds, url, err := cf("/x/a.creds", "/store", "", "").resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds != "/x/a.creds" || url != "" {
		t.Fatalf("resolve() = (%q,%q), want explicit creds and empty url (store discovery)", creds, url)
	}
}

func TestResolveFallsBackToActiveContext(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	mustSave(t, clictx.Context{Name: "alice", URL: "nats://ctx", Creds: "/ctx/a.creds"})
	mustSetActive(t, "alice")
	creds, url, err := cf("", "/store", "", "").resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds != "/ctx/a.creds" || url != "nats://ctx" {
		t.Fatalf("resolve() = (%q,%q), want active context's creds+url", creds, url)
	}
}

func TestResolveNamedContextOverridesActive(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	mustSave(t, clictx.Context{Name: "alice", URL: "nats://a", Creds: "/a.creds"})
	mustSetActive(t, "alice")
	mustSave(t, clictx.Context{Name: "bob", URL: "nats://b", Creds: "/b.creds"})
	creds, url, err := cf("", "/store", "", "bob").resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds != "/b.creds" || url != "nats://b" {
		t.Fatalf("resolve() = (%q,%q), want bob's creds+url", creds, url)
	}
}

func TestResolveURLFlagOverridesContextURL(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	mustSave(t, clictx.Context{Name: "alice", URL: "nats://ctx", Creds: "/a.creds"})
	mustSetActive(t, "alice")
	creds, url, err := cf("", "/store", "nats://override", "").resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds != "/a.creds" || url != "nats://override" {
		t.Fatalf("resolve() = (%q,%q), want context creds with --url override", creds, url)
	}
}

func TestResolveNoIdentityErrors(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if _, _, err := cf("", "/store", "", "").resolve(); !errors.Is(err, errNoIdentity) {
		t.Fatalf("resolve() err = %v, want errNoIdentity", err)
	}
}

func TestResolveMissingNamedContextErrors(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if _, _, err := cf("", "/store", "", "ghost").resolve(); !errors.Is(err, clictx.ErrNotFound) {
		t.Fatalf("resolve() err = %v, want ErrNotFound", err)
	}
}

func mustSave(t *testing.T, c clictx.Context) {
	t.Helper()
	if err := clictx.Save(c); err != nil {
		t.Fatalf("Save %s: %v", c.Name, err)
	}
}

func mustSetActive(t *testing.T, name string) {
	t.Helper()
	if err := clictx.SetActive(name); err != nil {
		t.Fatalf("SetActive %s: %v", name, err)
	}
}
