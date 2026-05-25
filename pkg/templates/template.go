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

// Mount class identifiers accepted in the template `mounts` field. The
// spawn handler resolves each class to a concrete container bind mount.
// New classes must be added here AND wired in the spawn handler;
// validation in Template.Validate guards against typo'd values like
// "shh". See specs/architecture.md §11b "Mount classes".
const (
	// MountClassWorktree → the agent's git worktree → /workspace.
	MountClassWorktree = "worktree"
	// MountClassSecrets → the per-template subset of
	// ~/.config/sextant/secrets/ → read-only mount. Stubbed; full
	// resolver lands with the secrets-store milestone.
	MountClassSecrets = "secrets"
	// MountClassSSH → host's ~/.ssh → /home/agent/.ssh read-only. Opt-in
	// so agents can `git push` over SSH. See
	// plans/issues/feat-container-ssh-passthrough.md and
	// specs/components/sidecar-image.md.
	MountClassSSH = "ssh"
)

// KnownMountClasses returns the sorted set of mount class strings the
// template loader accepts. Mirrored into error messages so a malformed
// template tells the operator exactly which values are valid.
func KnownMountClasses() []string {
	return []string{MountClassWorktree, MountClassSecrets, MountClassSSH}
}

// KnownMountClass reports whether name is one of the accepted mount
// class identifiers. Kept package-level so the spawn handler can reuse
// it when it inspects a template's mounts.
func KnownMountClass(name string) bool {
	for _, k := range KnownMountClasses() {
		if k == name {
			return true
		}
	}
	return false
}

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
	// ClaudeSeed is an optional host path that the spawn handler
	// surfaces into the container at /home/agent/.claude. Use it to
	// pre-load operator-curated CLAUDE.md, custom slash commands,
	// hooks, or settings.json for the agent class. Tilde (`~/`) is
	// expanded against os.UserHomeDir at validation time. When empty,
	// the spawn handler leaves /home/agent/.claude as the default
	// per-agent empty named volume.
	// See specs/architecture.md §11b,
	// plans/issues/feat-template-claude-seeding.md, and
	// plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md.
	ClaudeSeed string `toml:"claude_seed" json:"claude_seed,omitempty"`
	// ClaudeSeedMode controls how ClaudeSeed is surfaced to the agent:
	//
	//   - "" (unset) or "copy-on-spawn" (default when ClaudeSeed is set):
	//     at first spawn, sextantd creates a per-agent named volume
	//     `sextant-claude-seed-<uuid>`, populates it by copying the host
	//     seed dir contents, and mounts the volume rw at
	//     /home/agent/.claude. Subsequent spawns of the same agent re-
	//     attach the existing volume so the SDK's session journal in
	//     `projects/<encoded-cwd>/<session-id>.jsonl` survives restart.
	//     This is the right behavior for assistant-style agents.
	//
	//   - "readonly-bind": legacy behavior — bind-mount the host seed
	//     dir read-only at /home/agent/.claude. Suitable for one-shot
	//     agents that don't need the SDK to persist session state.
	//     Note: this mode *breaks* multi-turn session resume, because
	//     the SDK can't write `~/.claude/projects/.../*.jsonl`. Operators
	//     who pick this mode have explicitly opted into that trade.
	//
	// Ignored when ClaudeSeed is empty.
	ClaudeSeedMode string `toml:"claude_seed_mode" json:"claude_seed_mode,omitempty"`
}

// ClaudeSeed mode constants. These are the values ClaudeSeedMode
// accepts. Keep in sync with the doc comment on Template.ClaudeSeedMode
// and with specs/architecture.md §11b.
const (
	ClaudeSeedModeCopyOnSpawn = "copy-on-spawn"
	ClaudeSeedModeReadonly    = "readonly-bind"
)

// ResolveClaudeSeedMode returns the effective seed mode for a template.
// When ClaudeSeed is empty, the seed mode is meaningless; the empty
// string is returned. When ClaudeSeed is set and ClaudeSeedMode is
// blank, the default "copy-on-spawn" is returned (the right behavior
// for the assistant-style use case — see
// plans/issues/bug-claude-seed-readonly-breaks-session-persistence.md).
// Otherwise the explicit ClaudeSeedMode is returned verbatim (already
// validated at template load time).
func (t Template) ResolveClaudeSeedMode() string {
	if t.ClaudeSeed == "" {
		return ""
	}
	if t.ClaudeSeedMode == "" {
		return ClaudeSeedModeCopyOnSpawn
	}
	return t.ClaudeSeedMode
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
	// Mounts is an allowlist — unknown values fail loudly so a typo'd
	// class name (e.g. "shh" instead of "ssh") doesn't silently produce
	// an agent without the intended mount. See specs/architecture.md §11b
	// "Mount classes" for the resolved set.
	for _, m := range t.Mounts {
		if !KnownMountClass(m) {
			return fmt.Errorf("templates: unknown mount class %q (template %q); known: %s",
				m, t.Name, strings.Join(KnownMountClasses(), ", "))
		}
	}
	if t.ClaudeSeed != "" {
		expanded, err := ExpandClaudeSeed(t.ClaudeSeed)
		if err != nil {
			return fmt.Errorf("templates: claude_seed (template %q): %w", t.Name, err)
		}
		info, err := os.Stat(expanded)
		if err != nil {
			return fmt.Errorf("templates: claude_seed %q (template %q): %w", expanded, t.Name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("templates: claude_seed %q (template %q) is not a directory", expanded, t.Name)
		}
	}
	switch t.ClaudeSeedMode {
	case "", ClaudeSeedModeCopyOnSpawn, ClaudeSeedModeReadonly:
		// valid (empty means "use the copy-on-spawn default" — see
		// ResolveClaudeSeedMode).
	default:
		return fmt.Errorf("templates: claude_seed_mode must be %q or %q (template %q, got %q)",
			ClaudeSeedModeCopyOnSpawn, ClaudeSeedModeReadonly, t.Name, t.ClaudeSeedMode)
	}
	return nil
}

// ExpandClaudeSeed resolves a template's claude_seed field to an
// absolute on-host path. A leading `~/` (or bare `~`) expands to the
// invoking user's home directory via os.UserHomeDir; every other path
// is returned as-is. Splitting the expansion from Validate lets the
// spawn handler reuse the same logic when assembling the bind-mount
// without re-implementing the rule.
func ExpandClaudeSeed(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
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
