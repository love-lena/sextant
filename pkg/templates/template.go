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
	"time"

	"github.com/nats-io/nats.go"
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
	// slug:feat-container-ssh-passthrough and
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
	// slug:feat-template-claude-seeding, and
	// slug:bug-claude-seed-readonly-breaks-session-persistence.
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
// slug:bug-claude-seed-readonly-breaks-session-persistence).
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

// RetryPolicy controls how SyncToKVWithRetry handles transient NATS
// outages (the supervisor's restart-backoff window after a NATS crash).
// Zero value defaults to DefaultRetryPolicy via SyncToKVWithRetry.
type RetryPolicy struct {
	// Budget is the total wall-clock window allowed for retries. After
	// Budget elapses with no successful Put, the last underlying error
	// is returned wrapped. Defaults to 30s when zero — generous enough
	// to cover a NATS restart on a contended box without hanging the
	// daemon startup indefinitely.
	Budget time.Duration
	// Interval is the wait between retry attempts. Defaults to 200ms.
	// We use a fixed interval rather than exponential backoff because
	// the supervisor's restart window is short and bounded; constant
	// polling matches the "did NATS come back yet?" question better
	// than a backoff that overshoots the window.
	Interval time.Duration
}

// DefaultRetryPolicy returns the retry knobs used by SyncToKV /
// SyncDirToKV — the daemon-startup template sync path. The budget is
// sized to cover the supervisor's NATS restart (100ms-1s initial
// backoff + nats-server startup latency under load).
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Budget:   30 * time.Second,
		Interval: 200 * time.Millisecond,
	}
}

// IsTransientNATSError reports whether err is a transient NATS
// connection error that should trigger a retry. The supervisor will
// restart NATS within its backoff window; once the underlying
// nats.Conn reconnects (assuming reconnect options were set at
// Connect time) the next Put succeeds.
//
// Exported so the daemon's other startup steps (control.startControl,
// etc.) can branch on the same predicate when they grow their own
// retry loops.
func IsTransientNATSError(err error) bool {
	if err == nil {
		return false
	}
	// errors.Is matches the wrapped sentinel; nats.ErrConnectionClosed
	// is what nats.go returns from a Put on a closed connection.
	if errors.Is(err, nats.ErrConnectionClosed) {
		return true
	}
	// nats.ErrNoServers fires when the auto-reconnect loop has not yet
	// reattached. Same recovery path: wait for the supervisor to bring
	// NATS back, then re-try the Put.
	if errors.Is(err, nats.ErrNoServers) {
		return true
	}
	// Belt-and-suspenders for libraries that wrap but don't propagate
	// the sentinel: match by message. The string match is intentional
	// — nats.go's KV path occasionally returns a wrapped error with no
	// errors.Unwrap chain to the sentinel. Without the string match
	// the retry never triggers and the daemon dies on the first hiccup.
	msg := err.Error()
	if strings.Contains(msg, "nats: connection closed") {
		return true
	}
	if strings.Contains(msg, "nats: no servers available") {
		return true
	}
	return false
}

// SyncToKV writes every template in tpls into the `templates` KV bucket,
// JSON-encoded so other languages can read them later (TS client doesn't
// have a TOML parser bundled). Idempotent: re-Put on the same key
// overwrites, matching the §11b "re-run sextant init is the reload
// path" semantics.
//
// Transient `nats: connection closed` errors trigger a retry under
// DefaultRetryPolicy — sextantd startup races the NATS supervisor's
// restart window when the operator triggers a restart-during-startup
// (the bug-flake-daemon-restarts-nats-after-kill failure mode), and a
// retry absorbs the hiccup. Callers that want different timing should
// use SyncToKVWithRetry directly.
func SyncToKV(ctx context.Context, kv KV, tpls []Template) error {
	return SyncToKVWithRetry(ctx, kv, tpls, DefaultRetryPolicy())
}

// SyncToKVWithRetry is the policy-explicit version of SyncToKV. Each
// per-template Put is retried until the total budget elapses; only
// transient NATS errors trigger a retry, permanent errors (validation,
// JetStream bucket missing, etc.) surface immediately.
func SyncToKVWithRetry(ctx context.Context, kv KV, tpls []Template, policy RetryPolicy) error {
	if policy.Budget <= 0 {
		policy.Budget = DefaultRetryPolicy().Budget
	}
	if policy.Interval <= 0 {
		policy.Interval = DefaultRetryPolicy().Interval
	}
	for _, t := range tpls {
		if err := t.Validate(); err != nil {
			return err
		}
		raw, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("templates: marshal %q: %w", t.Name, err)
		}
		if err := putWithRetry(ctx, kv, t.Name, raw, policy); err != nil {
			return fmt.Errorf("templates: put %q: %w", t.Name, err)
		}
	}
	return nil
}

// putWithRetry retries a single Put on transient NATS errors. The
// underlying nats.Conn is expected to be reconnect-capable (the
// daemon's RPC/MCP connections are; see cmd/sextantd/rpc.go and
// cmd/sextantd/mcp.go) so the retry has a recovery path.
func putWithRetry(ctx context.Context, kv KV, key string, value []byte, policy RetryPolicy) error {
	deadline := time.Now().Add(policy.Budget)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("%w (context canceled mid-retry; last: %w)", err, lastErr)
			}
			return err
		}
		_, err := kv.Put(ctx, key, value)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsTransientNATSError(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("retry budget %s exceeded: %w", policy.Budget, lastErr)
		}
		select {
		case <-time.After(policy.Interval):
		case <-ctx.Done():
			return fmt.Errorf("%w (context canceled mid-retry; last: %w)", ctx.Err(), lastErr)
		}
	}
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
