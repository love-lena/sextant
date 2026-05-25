package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadRuntimeNATSURLReturnsLiveAddr — the happy path: runtime.json
// exists with a real nats_addr, helper returns the wrapped URL.
func TestReadRuntimeNATSURLReturnsLiveAddr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	body := `{"nats_addr":"127.0.0.1:53930","clickhouse_tcp":"127.0.0.1:9000"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	url, ok := readRuntimeNATSURL(path)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if url != "nats://127.0.0.1:53930" {
		t.Fatalf("url = %q, want nats://127.0.0.1:53930", url)
	}
}

// TestReadRuntimeNATSURLAbsentFallsBack — no runtime.json on disk; the
// helper signals "not available" via ok=false so Connect falls back to
// client.toml's URL.
func TestReadRuntimeNATSURLAbsentFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	url, ok := readRuntimeNATSURL(path)
	if ok {
		t.Fatalf("ok = true for missing file, want false (url=%q)", url)
	}
	if url != "" {
		t.Fatalf("url = %q for missing file, want empty", url)
	}
}

// TestReadRuntimeNATSURLMalformedFallsBack — runtime.json exists but is
// not valid JSON. The helper must NOT crash and must NOT bubble up an
// error; it returns ok=false so Connect transparently falls back. This
// is the load-bearing contract: a half-written runtime.json from a
// crashing daemon must never block the client.
func TestReadRuntimeNATSURLMalformedFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	url, ok := readRuntimeNATSURL(path)
	if ok {
		t.Fatalf("ok = true for malformed file, want false (url=%q)", url)
	}
	if url != "" {
		t.Fatalf("url = %q for malformed file, want empty", url)
	}
}

// TestReadRuntimeNATSURLMissingFieldFallsBack — valid JSON but the
// nats_addr field is absent. Same contract as the malformed case: fall
// back to client.toml.
func TestReadRuntimeNATSURLMissingFieldFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	body := `{"clickhouse_tcp":"127.0.0.1:9000"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	url, ok := readRuntimeNATSURL(path)
	if ok {
		t.Fatalf("ok = true with no nats_addr, want false (url=%q)", url)
	}
}

// TestDefaultRuntimePathRespectsHome — the canonical runtime path
// follows os.UserHomeDir (HOME on Unix), matching sextantd's
// DefaultPaths layout. Pinning this so callers (and the bug-fix test
// below) can redirect via t.Setenv("HOME", ...).
func TestDefaultRuntimePathRespectsHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := DefaultRuntimePath()
	if err != nil {
		t.Fatalf("DefaultRuntimePath: %v", err)
	}
	want := filepath.Join(tmp, ".local", "share", "sextant", "runtime.json")
	if got != want {
		t.Fatalf("DefaultRuntimePath = %q, want %q", got, want)
	}
}
