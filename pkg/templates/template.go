package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/pelletier/go-toml/v2"
)

// Bucket is the canonical KV bucket name for agent templates. Matches the
// row in pkg/natsboot/layout.go.
const Bucket = "templates"

// Template is the parsed shape of one agent-template TOML file. The
// schema is specs/architecture.md §11b. Fields are additive; new
// optional fields are safe to add without a wire break.
type Template struct {
	Name              string            `toml:"name" json:"name"`
	Description       string            `toml:"description" json:"description,omitempty"`
	Image             string            `toml:"image" json:"image"`
	Permissions       []string          `toml:"permissions" json:"permissions"`
	Env               map[string]string `toml:"env" json:"env,omitempty"`
	Mounts            []string          `toml:"mounts" json:"mounts,omitempty"`
	InitialPrompt     string            `toml:"initial_prompt" json:"initial_prompt,omitempty"`
	Model             string            `toml:"model" json:"model"`
	PermissionCeiling string            `toml:"permission_ceiling" json:"permission_ceiling,omitempty"`
}

// Validate asserts the invariants the spawn handler relies on: a name,
// an image, and a non-empty permissions list (the cap allowlist baked
// into the issued JWT). PermissionCeiling, when set, must be "auto" —
// the only mode initial supports.
func (t Template) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("templates: name is required")
	}
	if strings.TrimSpace(t.Image) == "" {
		return fmt.Errorf("templates: image is required (template %q)", t.Name)
	}
	if len(t.Permissions) == 0 {
		return fmt.Errorf("templates: permissions is required and must be non-empty (template %q)", t.Name)
	}
	switch t.PermissionCeiling {
	case "", "auto", "plan":
		// valid
	default:
		return fmt.Errorf("templates: permission_ceiling must be \"auto\" or \"plan\" (template %q, got %q)", t.Name, t.PermissionCeiling)
	}
	return nil
}

// LoadFromFile reads a single TOML file from path and returns the parsed
// Template. Returns ErrNotExist (wrapped) if the file is missing so
// callers can distinguish "no such template" from validation failures.
func LoadFromFile(path string) (Template, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-controlled path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Template{}, fmt.Errorf("templates: %s: %w", path, err)
		}
		return Template{}, fmt.Errorf("templates: read %s: %w", path, err)
	}
	var t Template
	if err := toml.Unmarshal(raw, &t); err != nil {
		return Template{}, fmt.Errorf("templates: parse %s: %w", path, err)
	}
	if t.Name == "" {
		// Derive the name from the file stem when the TOML doesn't say.
		// `default.toml` ⇒ `default`. Most files include `name = "..."`
		// but the fallback keeps the behavior unsurprising.
		base := filepath.Base(path)
		t.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if err := t.Validate(); err != nil {
		return Template{}, err
	}
	return t, nil
}

// LoadDir reads every `*.toml` under dir and returns the parsed
// templates in lexical-by-filename order. Files that fail to parse or
// validate fail the whole load — operator config errors should surface
// loudly, not silently drop a template.
func LoadDir(dir string) ([]Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("templates: read dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	out := make([]Template, 0, len(names))
	for _, name := range names {
		t, err := LoadFromFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// KV is the minimal NATS KV surface used to seed and resolve templates.
// Satisfied by the real jetstream.KeyValue and by fakes in tests.
type KV interface {
	Put(ctx context.Context, key string, value []byte) (uint64, error)
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error)
}

// SyncToKV writes every template in tpls into the `templates` KV bucket,
// JSON-encoded so other languages can read them later (TS client doesn't
// have a TOML parser bundled). Idempotent: re-Put on the same key
// overwrites, matching the §11b "re-run sextant init is the reload
// path" semantics.
func SyncToKV(ctx context.Context, kv KV, tpls []Template) error {
	for _, t := range tpls {
		if err := t.Validate(); err != nil {
			return err
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("templates: marshal %q: %w", t.Name, err)
		}
		if _, err := kv.Put(ctx, t.Name, raw); err != nil {
			return fmt.Errorf("templates: put %q: %w", t.Name, err)
		}
	}
	return nil
}

// SyncDirToKV reads every *.toml under dir and writes each into the
// `templates` KV bucket. Convenience wrapper for sextantd's startup
// sync (templates must exist in KV before the spawn handler can look
// them up). Returns the templates that were synced so callers can log
// the count.
//
// A missing or empty dir is a hard error — operators always run
// `sextant init` before sextantd, which seeds default.toml. Silently
// skipping would let a broken install look healthy.
func SyncDirToKV(ctx context.Context, kv KV, dir string) ([]Template, error) {
	tpls, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(tpls) == 0 {
		return nil, fmt.Errorf("templates: %s contains no *.toml files", dir)
	}
	if err := SyncToKV(ctx, kv, tpls); err != nil {
		return nil, err
	}
	return tpls, nil
}

// LoadFromKV returns the named template from the `templates` KV bucket.
// Returns ErrNotFound wrapped if the bucket has no entry for name.
func LoadFromKV(ctx context.Context, kv KV, name string) (Template, error) {
	if name == "" {
		return Template{}, fmt.Errorf("templates: name is empty")
	}
	entry, err := kv.Get(ctx, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return Template{}, fmt.Errorf("templates: %w: %s", ErrNotFound, name)
		}
		return Template{}, fmt.Errorf("templates: get %q: %w", name, err)
	}
	var t Template
	if err := json.Unmarshal(entry.Value(), &t); err != nil {
		return Template{}, fmt.Errorf("templates: decode %q: %w", name, err)
	}
	if err := t.Validate(); err != nil {
		return Template{}, fmt.Errorf("templates: stored template %q failed validation: %w", name, err)
	}
	return t, nil
}

// ErrNotFound signals "no template by that name." Surface to callers via
// errors.Is so they can render a clean error to the operator.
var ErrNotFound = errors.New("template not found")
