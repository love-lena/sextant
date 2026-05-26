package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// WorktreesBucket is the canonical KV bucket name for the worktree
// registry. See pkg/natsboot/layout.go.
const WorktreesBucket = "worktrees"

// MergeWorktreePrefix is the directory-name prefix the merge handler
// uses for its transient worktrees. Anything matching this prefix is
// considered safe to remove on next-merge cleanup. The prefix starts
// with `.` so a plain `ls` of the worktrees root doesn't surface it.
const MergeWorktreePrefix = ".merge-"

// ErrWorktreeNotFound signals a name not present in the registry.
var ErrWorktreeNotFound = errors.New("worktree: not found")

// ErrInvalidName signals a name that fails the
// conventions/git-workflow.md "Branch naming" rule.
var ErrInvalidName = errors.New("worktree: invalid name")

// ErrAlreadyExists signals a registry key collision.
var ErrAlreadyExists = errors.New("worktree: already exists")

// ErrStatusGuard signals an operation refused because the worktree's
// current status doesn't permit it (e.g. destroy without --force on
// an `active` worktree).
var ErrStatusGuard = errors.New("worktree: status guard")

// branchNamePattern enforces the `<kind>-<short-desc>-<seq>` rule
// from conventions/git-workflow.md. kind ∈ feat|fix|refactor|docs|
// test|chore|spec; desc is kebab-case (2-5 words, but we don't
// enforce the word count — just the shape); seq is 3 digits.
var branchNamePattern = regexp.MustCompile(`^(feat|fix|refactor|docs|test|chore|spec)-[a-z0-9]+(?:-[a-z0-9]+)*-\d{3}$`)

// Config bundles the inputs a Manager needs.
type Config struct {
	// RepoRoot is the path to the operator's main worktree (i.e. the
	// directory whose .git points at the bare repository or holds the
	// repository directly). Required.
	RepoRoot string

	// WorktreesRoot is the parent directory where per-task worktrees
	// land. Required; the directory is created if missing.
	WorktreesRoot string

	// Registry is the NATS KV bucket recording every live worktree.
	// Required.
	Registry RegistryKV

	// Locks is the NATS KV bucket the merge handler holds during a
	// merge. Required for Merge; tests that don't exercise Merge can
	// pass nil.
	Locks LockKV

	// HolderID is the identity the merge handler writes into
	// `locks.merge`. Typically the daemon's `<id>@<host>`. Required
	// when Locks is non-nil.
	HolderID string

	// MergeLockTTL bounds how long the merge handler holds the lock.
	// Zero falls back to DefaultMergeLockTTL.
	MergeLockTTL time.Duration

	// Now is injected for deterministic tests. Production passes
	// time.Now.
	Now func() time.Time
}

// RegistryKV is the narrow surface Manager needs on the `worktrees`
// bucket. Same shape as handlers.AgentMutableKV — kept separate so
// the worktree package doesn't depend on pkg/rpc/handlers.
type RegistryKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error)
	Put(ctx context.Context, key string, value []byte) (uint64, error)
	Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error
}

// Manager owns worktree-lifecycle operations. One per daemon.
type Manager struct {
	cfg Config
}

// New validates the config and returns a ready Manager. RepoRoot and
// WorktreesRoot must exist; the latter is created if missing.
func New(cfg Config) (*Manager, error) {
	if strings.TrimSpace(cfg.RepoRoot) == "" {
		return nil, fmt.Errorf("worktree: RepoRoot is required")
	}
	if strings.TrimSpace(cfg.WorktreesRoot) == "" {
		return nil, fmt.Errorf("worktree: WorktreesRoot is required")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("worktree: Registry is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MergeLockTTL <= 0 {
		cfg.MergeLockTTL = DefaultMergeLockTTL
	}
	if _, err := os.Stat(cfg.RepoRoot); err != nil {
		return nil, fmt.Errorf("worktree: stat RepoRoot %s: %w", cfg.RepoRoot, err)
	}
	if err := os.MkdirAll(cfg.WorktreesRoot, 0o750); err != nil {
		return nil, fmt.Errorf("worktree: mkdir WorktreesRoot %s: %w", cfg.WorktreesRoot, err)
	}
	return &Manager{cfg: cfg}, nil
}

// RepoRoot returns the operator's main repo path the manager was
// configured with. Exported so callers (Pruner, tests) can compose
// against the same path without re-deriving it.
func (m *Manager) RepoRoot() string { return m.cfg.RepoRoot }

// WorktreesRoot returns the directory where per-task worktrees land.
// Exported for the same reason as RepoRoot.
func (m *Manager) WorktreesRoot() string { return m.cfg.WorktreesRoot }

// ValidateName returns nil if name matches the branch-naming convention.
// Exported so callers (CLI, MCP) can fail-fast before calling Create.
func ValidateName(name string) error {
	if !branchNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q does not match <kind>-<desc>-<seq> (kind ∈ feat|fix|refactor|docs|test|chore|spec, seq=NNN)", ErrInvalidName, name)
	}
	return nil
}

// Create makes a new worktree at <WorktreesRoot>/<name>/ on a fresh
// branch <name> off <baseBranch>. Registers it in KV. The owningAgent
// UUID is recorded for "who owns this worktree?" — zero UUID means
// operator-created. Returns the registered WorktreeInfo.
//
// baseBranch defaults to "main" when empty.
func (m *Manager) Create(ctx context.Context, name, baseBranch string, owningAgent uuid.UUID) (sextantproto.WorktreeInfo, error) {
	if err := ValidateName(name); err != nil {
		return sextantproto.WorktreeInfo{}, err
	}
	if baseBranch == "" {
		baseBranch = "main"
	}

	// Reject duplicates against the registry. Use a Get probe — Put is
	// last-write-wins so we'd silently overwrite without it.
	if _, err := m.cfg.Registry.Get(ctx, name); err == nil {
		return sextantproto.WorktreeInfo{}, fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: probe registry: %w", err)
	}

	path := filepath.Join(m.cfg.WorktreesRoot, name)
	// Refuse to overwrite a path that already exists on disk — even if
	// the registry doesn't know about it, removing a stranger's
	// directory silently is too easy a way to lose work.
	if _, err := os.Stat(path); err == nil {
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: target path %s already exists on disk", path)
	}

	now := m.cfg.Now().UTC()

	if err := runGit(ctx, m.cfg.RepoRoot, "worktree", "add", "-b", name, path, baseBranch); err != nil {
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: git worktree add: %w", err)
	}

	info := sextantproto.WorktreeInfo{
		Name:         name,
		Path:         path,
		Branch:       name,
		BaseBranch:   baseBranch,
		OwningAgent:  owningAgent,
		Status:       sextantproto.WorktreeStatusActive,
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := m.putInfo(ctx, info); err != nil {
		// Roll back the on-disk worktree so we don't accumulate
		// orphans.
		_ = runGit(context.Background(), m.cfg.RepoRoot, "worktree", "remove", "--force", path) //nolint:contextcheck // cleanup outlives ctx
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: persist registry: %w", err)
	}
	return info, nil
}

// Destroy removes a worktree's directory and registry entry. By
// default it refuses to act on a non-archived worktree; force=true
// bypasses the guard (operator override).
func (m *Manager) Destroy(ctx context.Context, name string, force bool) error {
	info, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if !force && info.Status != sextantproto.WorktreeStatusArchived &&
		info.Status != sextantproto.WorktreeStatusMerged {
		return fmt.Errorf("%w: worktree %q status=%s; use force=true to destroy anyway",
			ErrStatusGuard, name, info.Status)
	}

	// `git worktree remove` refuses if the worktree has uncommitted
	// state. force=true here maps to git's --force; matches the
	// caller's intent.
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, info.Path)
	if err := runGit(ctx, m.cfg.RepoRoot, args...); err != nil {
		return fmt.Errorf("worktree: git worktree remove: %w", err)
	}
	if err := m.cfg.Registry.Delete(ctx, name); err != nil {
		return fmt.Errorf("worktree: delete registry entry: %w", err)
	}
	return nil
}

// List returns every worktree in the registry. Ordering is by Name
// for predictable output. Empty result is a non-nil empty slice.
func (m *Manager) List(ctx context.Context) ([]sextantproto.WorktreeInfo, error) {
	lister, err := m.cfg.Registry.ListKeys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) || errors.Is(err, jetstream.ErrKeyNotFound) {
			return []sextantproto.WorktreeInfo{}, nil
		}
		return nil, fmt.Errorf("worktree: list keys: %w", err)
	}
	defer func() { _ = lister.Stop() }()

	var out []sextantproto.WorktreeInfo
	for key := range lister.Keys() {
		entry, err := m.cfg.Registry.Get(ctx, key)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				continue
			}
			return nil, fmt.Errorf("worktree: get %s: %w", key, err)
		}
		var info sextantproto.WorktreeInfo
		if err := json.Unmarshal(entry.Value(), &info); err != nil {
			// Garbage in the registry — skip but don't fail the whole
			// list. The corruption surfaces in audit log of the writer
			// that produced it.
			continue
		}
		out = append(out, info)
	}
	if out == nil {
		out = []sextantproto.WorktreeInfo{}
	}
	// Stable order by Name. List is small (dozens of entries at most)
	// so an insertion sort is plenty.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// Get returns one worktree's registry entry. Returns
// ErrWorktreeNotFound when the key is absent.
func (m *Manager) Get(ctx context.Context, name string) (sextantproto.WorktreeInfo, error) {
	entry, err := m.cfg.Registry.Get(ctx, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return sextantproto.WorktreeInfo{}, fmt.Errorf("%w: %s", ErrWorktreeNotFound, name)
		}
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: get %s: %w", name, err)
	}
	var info sextantproto.WorktreeInfo
	if err := json.Unmarshal(entry.Value(), &info); err != nil {
		return sextantproto.WorktreeInfo{}, fmt.Errorf("worktree: decode %s: %w", name, err)
	}
	return info, nil
}

// Diff returns `git diff <against>...<branch>` rendered as a string.
// against defaults to "main" when empty. The diff is the
// triple-dot variant — patches showing what <name> introduced
// relative to its merge base with <against>.
func (m *Manager) Diff(ctx context.Context, name, against string) (string, error) {
	if against == "" {
		against = "main"
	}
	info, err := m.Get(ctx, name)
	if err != nil {
		return "", err
	}
	out, _, err := runGitOut(ctx, m.cfg.RepoRoot, "diff", against+"..."+info.Branch)
	if err != nil {
		return "", fmt.Errorf("worktree: git diff %s...%s: %w", against, info.Branch, err)
	}
	return out, nil
}

// MergeResult is the structured outcome of a Merge call. Conflicts is
// non-empty on a conflicted merge; empty (nil) on a clean merge.
type MergeResult struct {
	OK        bool
	Branch    string
	Target    string
	Conflicts []string
}

// Merge merges a worktree's branch into target under the
// `locks.merge` lock. target defaults to "main".
//
// Implementation: dedicated transient merge worktree. See
// specs/architecture.md §11 "Merge strategy".
func (m *Manager) Merge(ctx context.Context, name, target string) (MergeResult, error) {
	if m.cfg.Locks == nil {
		return MergeResult{}, fmt.Errorf("worktree: Locks KV is required for Merge")
	}
	if target == "" {
		target = "main"
	}
	// Pre-lock probe so we fail fast on an unknown name without
	// taking the lock. The state read here is advisory only —
	// between this Get and the lock acquire another caller may have
	// completed a merge of the same worktree, so we re-Get under
	// the lock below before relying on Status.
	if _, err := m.Get(ctx, name); err != nil {
		return MergeResult{}, err
	}

	// Acquire the merge lock with bounded wait. `conventions/git-
	// workflow.md` §"Merging" step 1 says "Acquire locks.merge (or
	// wait)" — a Merge call by an agent that loses the race to a
	// peer should park, not error. The wait is bounded by one TTL;
	// past that the existing holder is treated as stale and
	// reclaimed by AcquireMergeLock itself.
	release, err := acquireMergeLockWithWait(ctx, m.cfg.Locks, m.cfg.HolderID, m.cfg.MergeLockTTL, m.cfg.Now)
	if err != nil {
		return MergeResult{}, err
	}
	// release runs unconditionally. Marking the lock as released
	// before returning the result avoids holding it across the audit
	// envelope writer in the RPC handler.
	defer func() { _ = release() }()

	// Re-Get under the lock. Closes the TOCTOU window between the
	// pre-lock probe and the lock acquire: another caller could have
	// merged the same worktree between our probe and our lock-grab.
	// If the current Status is `merged`, we no-op (idempotent); any
	// other unexpected status falls through to the merge attempt
	// (git will report a conflict / no-op as appropriate).
	info, err := m.Get(ctx, name)
	if err != nil {
		return MergeResult{}, err
	}
	if info.Status == sextantproto.WorktreeStatusMerged {
		return MergeResult{OK: true, Branch: info.Branch, Target: target}, nil
	}

	// Mark the source worktree as merging while we hold the lock.
	info.Status = sextantproto.WorktreeStatusMerging
	info.LastActivity = m.cfg.Now().UTC()
	_ = m.putInfo(ctx, info) // best-effort; a KV failure here doesn't void the merge

	// Clean up stale transient worktrees from prior crashed merges
	// before allocating a new one.
	m.cleanupStaleMergeWorktrees(ctx)

	mergeDir, err := m.allocMergeWorktreeDir()
	if err != nil {
		m.markStatus(ctx, info, sextantproto.WorktreeStatusActive)
		return MergeResult{}, fmt.Errorf("worktree: alloc merge dir: %w", err)
	}

	// `git worktree add <path> <target>` — checks out target into the
	// transient worktree. Reuse-or-fail semantics: if the branch is
	// already checked out elsewhere, this errors. For target=main on a
	// fresh clone the user's main worktree is on `main`, which would
	// trip git's "branch already checked out" check. We bypass by
	// detaching: --detach gives us a detached HEAD at the target's tip;
	// the actual ref-update is via `git update-ref` after the merge
	// commit lands.
	if err := runGit(ctx, m.cfg.RepoRoot, "worktree", "add", "--detach", mergeDir, target); err != nil {
		m.markStatus(ctx, info, sextantproto.WorktreeStatusActive)
		return MergeResult{}, fmt.Errorf("worktree: create merge worktree: %w", err)
	}
	// Always tear down the merge worktree. cleanup intentionally uses
	// a fresh background ctx so a canceled request still releases the
	// transient worktree.
	//nolint:contextcheck // cleanup intentionally uses background ctx
	defer func() {
		_ = runGit(context.Background(), m.cfg.RepoRoot, "worktree", "remove", "--force", mergeDir)
	}()

	// Configure a minimal git identity in the merge worktree so
	// `git merge --no-ff` can write a commit. We don't want to depend
	// on operator's global git config existing in CI / fresh
	// environments.
	if err := runGit(ctx, mergeDir, "-c", "user.email=sextantd@local", "-c", "user.name=sextantd",
		"merge", "--no-ff", "-m", fmt.Sprintf("Merge worktree %s into %s", info.Branch, target),
		info.Branch); err != nil {
		// Conflict path — collect conflicted files and abort the merge.
		conflicts, _ := m.listConflicts(ctx, mergeDir)
		_ = runGit(context.Background(), mergeDir, "merge", "--abort") //nolint:contextcheck // cleanup outlives ctx
		info.Status = sextantproto.WorktreeStatusConflict
		info.LastActivity = m.cfg.Now().UTC()
		_ = m.putInfo(ctx, info)
		return MergeResult{
			OK:        false,
			Branch:    info.Branch,
			Target:    target,
			Conflicts: conflicts,
		}, nil
	}

	// Clean merge — the merge commit landed on the detached HEAD.
	// Advance the target ref to the new HEAD.
	headSHA, _, err := runGitOut(ctx, mergeDir, "rev-parse", "HEAD")
	if err != nil {
		m.markStatus(ctx, info, sextantproto.WorktreeStatusActive)
		return MergeResult{}, fmt.Errorf("worktree: rev-parse HEAD after merge: %w", err)
	}
	headSHA = strings.TrimSpace(headSHA)
	if err := runGit(ctx, m.cfg.RepoRoot, "update-ref", "refs/heads/"+target, headSHA); err != nil {
		m.markStatus(ctx, info, sextantproto.WorktreeStatusActive)
		return MergeResult{}, fmt.Errorf("worktree: update-ref %s: %w", target, err)
	}

	info.Status = sextantproto.WorktreeStatusMerged
	info.LastActivity = m.cfg.Now().UTC()
	if err := m.putInfo(ctx, info); err != nil {
		// Merge already landed; KV update failure is a soft error.
		// Return success with a synthesized error so the caller can log
		// it but doesn't think the merge failed.
		return MergeResult{OK: true, Branch: info.Branch, Target: target}, fmt.Errorf("worktree: post-merge KV update: %w", err)
	}

	return MergeResult{OK: true, Branch: info.Branch, Target: target}, nil
}

// putInfo marshals + writes one WorktreeInfo into the registry.
func (m *Manager) putInfo(ctx context.Context, info sextantproto.WorktreeInfo) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("worktree: marshal info: %w", err)
	}
	if _, err := m.cfg.Registry.Put(ctx, info.Name, raw); err != nil {
		return fmt.Errorf("worktree: registry put %s: %w", info.Name, err)
	}
	return nil
}

// markStatus is a best-effort status-revert used on rollback paths.
// Failures are logged into the returned error by the caller; we
// don't surface them here.
func (m *Manager) markStatus(ctx context.Context, info sextantproto.WorktreeInfo, status sextantproto.WorktreeStatus) {
	info.Status = status
	info.LastActivity = m.cfg.Now().UTC()
	_ = m.putInfo(ctx, info)
}

// allocMergeWorktreeDir returns a path under WorktreesRoot that does
// not yet exist. The name includes a short random suffix so two
// merge attempts back-to-back don't reuse the same dir name.
func (m *Manager) allocMergeWorktreeDir() (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("rand: %w", err)
		}
		suffix := hex.EncodeToString(buf)
		dir := filepath.Join(m.cfg.WorktreesRoot, MergeWorktreePrefix+suffix)
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("worktree: could not allocate merge dir after 8 attempts")
}

// cleanupStaleMergeWorktrees removes any `.merge-*` directory left
// over from a crashed prior merge. Called inline before each merge so
// the daemon doesn't need a separate sweeper goroutine.
func (m *Manager) cleanupStaleMergeWorktrees(ctx context.Context) {
	entries, err := os.ReadDir(m.cfg.WorktreesRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), MergeWorktreePrefix) {
			continue
		}
		path := filepath.Join(m.cfg.WorktreesRoot, e.Name())
		// `git worktree remove --force` cleans up both the dir and the
		// .git/worktrees/<name> bookkeeping. If that fails, fall back
		// to a plain RemoveAll so we at least free the disk.
		if err := runGit(ctx, m.cfg.RepoRoot, "worktree", "remove", "--force", path); err != nil {
			_ = os.RemoveAll(path)
		}
	}
	// `git worktree prune` removes any stale bookkeeping where the
	// directory was deleted but the .git pointer remains.
	_ = runGit(ctx, m.cfg.RepoRoot, "worktree", "prune")
}

// listConflicts returns the list of paths git reports as unmerged
// (output of `git diff --name-only --diff-filter=U`). Empty on clean
// state.
func (m *Manager) listConflicts(ctx context.Context, worktreeDir string) ([]string, error) {
	out, _, err := runGitOut(ctx, worktreeDir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	conflicts := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			conflicts = append(conflicts, ln)
		}
	}
	return conflicts, nil
}

// runGit runs `git <args>` against the given directory with stdout
// and stderr discarded except in the error message.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are caller-controlled, callers vet them
	cmd.Dir = dir
	// Inherit the daemon's env so HOME / git config still applies.
	// Disabling git prompts so a missing credential doesn't hang.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runGitOut runs `git <args>` and captures stdout (combined with
// stderr in the error message on failure).
func runGitOut(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are caller-controlled, callers vet them
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", string(ee.Stderr), fmt.Errorf("git %s: %w (stderr: %s)",
				strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), "", nil
}

// SpawnWorktreeName builds the name an agent-spawn worktree should
// take. See specs/architecture.md §11 "Worktree naming". Exported so
// the spawn handler can compose its name without re-implementing the
// rule.
func SpawnWorktreeName(templateName string, agentUUID uuid.UUID) string {
	short := agentUUID.String()
	if len(short) > 8 {
		short = short[:8]
	}
	// Spawn worktrees skip the strict <kind>-<desc>-<seq> validation —
	// they're not source-of-truth task branches, they're per-agent
	// scratch space. We still produce a name that *looks* like the
	// convention so a human reading `worktree_list` doesn't have to
	// learn two formats.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, templateName)
	if safe == "" {
		safe = "agent"
	}
	return fmt.Sprintf("feat-%s-%s-001", safe, short)
}
