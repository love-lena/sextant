package clictx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/shared/go/clictx"
)

func TestInvalidNamesRejected(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	for _, bad := range []string{"", ".", "..", "../escape", "a/b", `a\b`} {
		if err := clictx.Save(clictx.Context{Name: bad, URL: "u", Creds: "c"}); err == nil {
			t.Errorf("Save(%q) accepted an illegal name", bad)
		}
		if _, err := clictx.Load(bad); err == nil {
			t.Errorf("Load(%q) accepted an illegal name", bad)
		}
		if err := clictx.Delete(bad); err == nil {
			t.Errorf("Delete(%q) accepted an illegal name", bad)
		}
		if _, err := clictx.WriteCreds(bad, "x"); err == nil {
			t.Errorf("WriteCreds(%q) accepted an illegal name", bad)
		}
	}
}

func TestListSkipsCorruptEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SEXTANT_HOME", home)
	if err := clictx.Save(clictx.Context{Name: "good", URL: "u", Creds: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// A malformed record must be skipped, not fail the whole listing.
	if err := os.WriteFile(filepath.Join(home, "context", "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := clictx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("List() = %+v, want only the good entry", got)
	}
}

func TestRootHonorsSextantHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SEXTANT_HOME", dir)
	if got := clictx.Root(); got != dir {
		t.Fatalf("Root() = %q, want $SEXTANT_HOME %q", got, dir)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	in := clictx.Context{
		Name:    "alice@local",
		URL:     "nats://127.0.0.1:4222",
		ID:      "01ABCDEF",
		Display: "alice",
		Kind:    "worker",
		Creds:   "/tmp/alice.creds",
	}
	if err := clictx.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := clictx.Load("alice@local")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != in {
		t.Fatalf("Load() = %+v, want %+v", got, in)
	}
}

func TestLoadMissingReturnsNotFound(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if _, err := clictx.Load("nope"); !errors.Is(err, clictx.ErrNotFound) {
		t.Fatalf("Load(missing) err = %v, want ErrNotFound", err)
	}
}

func TestListSortedByName(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	for _, n := range []string{"charlie", "alice", "bob"} {
		if err := clictx.Save(clictx.Context{Name: n, URL: "u", Creds: "c"}); err != nil {
			t.Fatalf("Save %s: %v", n, err)
		}
	}
	got, err := clictx.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alice", "bob", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("List() len = %d, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Name != want[i] {
			t.Fatalf("List()[%d].Name = %q, want %q", i, c.Name, want[i])
		}
	}
}

func TestListEmptyIsEmptyNotError(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	got, err := clictx.List()
	if err != nil {
		t.Fatalf("List() on empty home: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %v, want empty", got)
	}
}

func TestActiveDefaultsEmpty(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if got := clictx.Active(); got != "" {
		t.Fatalf("Active() = %q, want empty when none set", got)
	}
}

func TestSetActiveThenActive(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "alice", URL: "u", Creds: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := clictx.SetActive("alice"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := clictx.Active(); got != "alice" {
		t.Fatalf("Active() = %q, want alice", got)
	}
}

func TestSetActiveMissingErrors(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.SetActive("ghost"); !errors.Is(err, clictx.ErrNotFound) {
		t.Fatalf("SetActive(missing) err = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesAndClearsActive(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "alice", URL: "u", Creds: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := clictx.SetActive("alice"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := clictx.Delete("alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := clictx.Load("alice"); !errors.Is(err, clictx.ErrNotFound) {
		t.Fatalf("Load after delete err = %v, want ErrNotFound", err)
	}
	if got := clictx.Active(); got != "" {
		t.Fatalf("Active() = %q after deleting the active context, want empty", got)
	}
}

func TestDeleteMissingReturnsNotFound(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Delete("ghost"); !errors.Is(err, clictx.ErrNotFound) {
		t.Fatalf("Delete(missing) err = %v, want ErrNotFound", err)
	}
}

func TestWriteCredsIsPrivate(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	path, err := clictx.WriteCreds("alice", "CREDS-BLOB")
	if err != nil {
		t.Fatalf("WriteCreds: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if string(b) != "CREDS-BLOB" {
		t.Fatalf("creds content = %q, want CREDS-BLOB", b)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("creds perm = %o, want 600", perm)
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "alpha", URL: "nats://ctx:4222", Creds: "/tmp/alpha.creds"}); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit creds win", func(t *testing.T) {
		rc, err := clictx.Resolve("/tmp/explicit.creds", "nats://flag:4222", "alpha")
		if err != nil {
			t.Fatal(err)
		}
		if rc.Creds != "/tmp/explicit.creds" || rc.URL != "nats://flag:4222" || rc.Context != "" {
			t.Fatalf("got %+v", rc)
		}
	})

	t.Run("named context supplies creds and url", func(t *testing.T) {
		rc, err := clictx.Resolve("", "", "alpha")
		if err != nil {
			t.Fatal(err)
		}
		if rc.Creds != "/tmp/alpha.creds" || rc.URL != "nats://ctx:4222" || rc.Context != "alpha" {
			t.Fatalf("got %+v", rc)
		}
	})

	t.Run("explicit url overrides context url", func(t *testing.T) {
		rc, err := clictx.Resolve("", "nats://flag:4222", "alpha")
		if err != nil {
			t.Fatal(err)
		}
		if rc.URL != "nats://flag:4222" || rc.Creds != "/tmp/alpha.creds" {
			t.Fatalf("got %+v", rc)
		}
	})

	t.Run("active context is the fallback", func(t *testing.T) {
		if err := clictx.SetActive("alpha"); err != nil {
			t.Fatal(err)
		}
		rc, err := clictx.Resolve("", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.Context != "alpha" {
			t.Fatalf("got %+v", rc)
		}
	})

	t.Run("missing named context errors", func(t *testing.T) {
		if _, err := clictx.Resolve("", "", "ghost"); err == nil || !errors.Is(err, clictx.ErrNotFound) {
			t.Fatalf("err = %v, want clictx.ErrNotFound", err)
		}
	})
}

func TestResolveNoIdentity(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir()) // no contexts, no active
	if _, err := clictx.Resolve("", "", ""); !errors.Is(err, clictx.ErrNoIdentity) {
		t.Fatalf("err = %v, want clictx.ErrNoIdentity", err)
	}
}
