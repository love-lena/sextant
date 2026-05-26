package sextantd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/templates"
)

const tplDefault = `name = "default"
description = "Default template."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.prompt"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
permission_ceiling = "auto"
`

const tplLead = `name = "lead"
description = "Lead template."
image = "sextant-sidecar:latest"
permissions = ["read.agents", "control.spawn"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
`

const tplDev = `name = "dev"
description = "Dev template."
image = "sextant-sidecar:latest"
permissions = ["read.agents"]
mounts = ["worktree"]
model = "claude-opus-4-7[1m]"
`

// TestTemplatesReloadHandlerCount drives ReloadTemplates with a fake KV
// across three template files and asserts the returned count matches.
// Catches the wiring between SyncDirToKV's slice return and the count
// the CLI prints — a regression here would have the operator see
// "synced 0 template(s)" while KV silently held N.
func TestTemplatesReloadHandlerCount(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"default.toml": tplDefault,
		"lead.toml":    tplLead,
		"dev.toml":     tplDev,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	kv := newMemKV()
	count, err := ReloadTemplates(context.Background(), kv, dir)
	if err != nil {
		t.Fatalf("ReloadTemplates: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	// The KV must hold one entry per template name (SyncDirToKV stores
	// each under its template name as the key).
	for _, name := range []string{"default", "lead", "dev"} {
		if _, ok := kv.get(name); !ok {
			t.Errorf("kv missing key %q", name)
		}
	}
}

// TestTemplatesReloadHandlerEmptyDirError exercises the error path: an
// empty templates dir should not silently succeed with count=0.
// SyncDirToKV's contract is "no *.toml → error"; ReloadTemplates must
// surface that.
func TestTemplatesReloadHandlerEmptyDirError(t *testing.T) {
	dir := t.TempDir()
	kv := newMemKV()
	count, err := ReloadTemplates(context.Background(), kv, dir)
	if err == nil {
		t.Fatal("expected error for empty templates dir")
	}
	if count != 0 {
		t.Errorf("count on error = %d, want 0", count)
	}
}

// TestTemplatesReloadHandlerNilGuards documents the precondition checks
// the daemon-side subscriber relies on so a misconfigured caller gets a
// loud failure rather than a panic.
func TestTemplatesReloadHandlerNilGuards(t *testing.T) {
	if _, err := ReloadTemplates(context.Background(), nil, "/tmp"); err == nil {
		t.Error("expected error for nil KV")
	}
	if _, err := ReloadTemplates(context.Background(), newMemKV(), ""); err == nil {
		t.Error("expected error for empty dir")
	}
}

// --- memKV: in-memory fake KV satisfying templates.KV for unit tests.

type memKV struct {
	mu      sync.Mutex
	entries map[string][]byte
}

func newMemKV() *memKV { return &memKV{entries: make(map[string][]byte)} }

func (m *memKV) Put(_ context.Context, key string, value []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = append([]byte(nil), value...)
	return uint64(len(m.entries)), nil
}

func (m *memKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return memEntry{key: key, value: v}, nil
}

func (m *memKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	return nil, errors.New("memKV: ListKeys not implemented")
}

func (m *memKV) get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.entries[key]
	return v, ok
}

type memEntry struct {
	key   string
	value []byte
}

func (e memEntry) Key() string                     { return e.key }
func (e memEntry) Value() []byte                   { return e.value }
func (e memEntry) Bucket() string                  { return templates.Bucket }
func (e memEntry) Revision() uint64                { return 1 }
func (e memEntry) Created() time.Time              { return time.Time{} }
func (e memEntry) Delta() uint64                   { return 0 }
func (e memEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// Ensure memKV satisfies the surface ReloadTemplates needs.
var _ templates.KV = (*memKV)(nil)
