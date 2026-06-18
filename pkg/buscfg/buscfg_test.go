package buscfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := Path(t.TempDir())
	in := Config{LeafListen: "127.0.0.1:7422"}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestLoadMissingIsDefaultOff(t *testing.T) {
	// An absent config is the default-off case, not a failure: a fresh install
	// must run `up` identically to today without a config file present.
	got, err := Load(Path(t.TempDir())) // dir exists, file does not
	if err != nil {
		t.Fatalf("Load(missing): unexpected error %v", err)
	}
	if got != (Config{}) {
		t.Errorf("Load(missing) = %+v, want zero Config", got)
	}
}

func TestLoadMalformedIsError(t *testing.T) {
	// A present-but-broken config must fail loud — never silently fall back to
	// default-off (which would start the bus without the configured leaf).
	path := Path(t.TempDir())
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load(malformed): want error, got nil")
	}
}

func TestSaveCreatesStoreDir(t *testing.T) {
	// Save targets the store dir; if it does not exist yet (e.g. `config set`
	// before the first `up`), Save creates it rather than erroring.
	path := filepath.Join(t.TempDir(), "store", DefaultFile)
	if err := Save(path, Config{LeafListen: "127.0.0.1:7422"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}
}

func TestEmptyLeafListenOmitted(t *testing.T) {
	// A cleared leaf-listen ("") round-trips as default-off.
	path := Path(t.TempDir())
	if err := Save(path, Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.LeafListen != "" {
		t.Errorf("LeafListen = %q, want empty", out.LeafListen)
	}
}
