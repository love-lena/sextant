package main

// TASK-256 proof: per-run isolated git worktree provisioning. These tests drive the
// REAL provisioning helpers (provisionWorktree / teardownWorktree) against a throwaway
// git repo created in the test — no mock git. They assert the worktree PROPERTIES the
// ACs name, not just that a directory exists:
//
//   - AC#1: the provisioned path is a LINKED worktree under `git worktree list` of the
//     target repo, on a fresh branch distinct from the repo's default — NOT a scratch
//     dir with its own `.git init` (the fake-pass guard).
//   - AC#2 (mechanical half): two run ids against the same repo get DISTINCT worktree
//     paths AND independent branches — a commit on one branch does not appear on the
//     other (no cross-contamination). The LIVE concurrent-run half is gated on the
//     concurrency/adoption work (TASK-258/259); this is the mechanical property that
//     lets it pass, asserted here.
//   - AC#3: after teardown the repo's `git worktree list` shows NO net growth — the
//     entry is both removed AND pruned (an orphaned admin entry whose dir is gone is
//     pruned, the fake-pass guard).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs git in dir and fails the test on error, returning trimmed stdout.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo creates a throwaway git repo with one commit and returns its path. The repo
// is the TARGET a run branches from — it stands in for the operator's real repo, but is
// entirely owned by the test (a temp dir), so nothing here touches a real checkout.
func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q")
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	// A commit so HEAD is a real ref the worktree can branch from.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "-q", "-m", "seed")
	return repo
}

// worktreeList returns the `git worktree list --porcelain` worktree paths of repo.
func worktreeList(t *testing.T, repo string) []string {
	t.Helper()
	out := mustGit(t, repo, "worktree", "list", "--porcelain")
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths
}

// resolvedContains reports whether want resolves to the same file as any path in list
// (tolerating the symlink resolution git applies — macOS /var → /private/var).
func resolvedContains(list []string, want string) bool {
	for _, p := range list {
		if p == want || sameFile(p, want) {
			return true
		}
	}
	return false
}

// TestProvisionWorktree_RealLinkedWorktree (AC#1): the provisioned path is a real
// linked worktree of the target repo on a fresh branch, NOT a scratch dir.
func TestProvisionWorktree_RealLinkedWorktree(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	store := filepath.Join(t.TempDir(), "store") // its parent is the scratch neighbourhood
	runID := "01RUNAAAAAAAAAAAAAAAAAAAAA"

	path, err := provisionWorktree(ctx, repo, "", store, runID)
	if err != nil {
		t.Fatalf("provisionWorktree: %v", err)
	}

	// PROPERTY (fake-pass guard): the path is a LINKED worktree under the TARGET repo's
	// `git worktree list` — not merely a directory that exists, and not its own `.git
	// init`. We query the target repo, not the path itself.
	if got := worktreeList(t, repo); !resolvedContains(got, path) {
		t.Fatalf("provisioned path %q is not a linked worktree of %q; worktree list = %v", path, repo, got)
	}

	// PROPERTY: the worktree is on a fresh branch namespaced to the run, distinct from
	// the repo's default branch.
	branch := mustGit(t, path, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != runBranch(runID) {
		t.Fatalf("worktree branch = %q; want %q", branch, runBranch(runID))
	}
	defaultBranch := mustGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == defaultBranch {
		t.Fatalf("worktree branch %q must be distinct from the repo's default %q", branch, defaultBranch)
	}

	// PROPERTY: the worktree lives under the scratch dir (a sibling of the store), never
	// inside the target repo's own tree (no contamination of the operator's checkout).
	if !strings.HasPrefix(path, worktreeScratchDir(store)) {
		t.Fatalf("worktree %q is not under the scratch dir %q", path, worktreeScratchDir(store))
	}
	if rel, err := filepath.Rel(repo, path); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("worktree %q must NOT be inside the target repo %q (rel=%q)", path, repo, rel)
	}
}

// TestProvisionWorktree_DistinctAndIsolated (AC#2 mechanical half): two run ids against
// the same repo get DISTINCT paths and independent branches — a commit on one is invisible
// on the other (no cross-contamination).
func TestProvisionWorktree_DistinctAndIsolated(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	store := filepath.Join(t.TempDir(), "store")
	runA := "01RUNAAAAAAAAAAAAAAAAAAAAA"
	runB := "01RUNBBBBBBBBBBBBBBBBBBBBB"

	pathA, err := provisionWorktree(ctx, repo, "", store, runA)
	if err != nil {
		t.Fatalf("provisionWorktree A: %v", err)
	}
	pathB, err := provisionWorktree(ctx, repo, "", store, runB)
	if err != nil {
		t.Fatalf("provisionWorktree B: %v", err)
	}

	// PROPERTY: distinct paths (no run shares a worktree path).
	if pathA == pathB {
		t.Fatalf("two runs got the SAME worktree path %q — must be distinct", pathA)
	}

	// Make a commit on A only.
	if err := os.WriteFile(filepath.Join(pathA, "a.txt"), []byte("only-on-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, pathA, "add", "a.txt")
	mustGit(t, pathA, "commit", "-q", "-m", "work on A")

	// PROPERTY (no cross-contamination): A's commit is on A's branch but NOT on B's. The
	// file does not exist in B's tree, and B's log carries no "work on A".
	if _, err := os.Stat(filepath.Join(pathB, "a.txt")); err == nil {
		t.Fatalf("run A's file a.txt leaked into run B's worktree %q — cross-contamination", pathB)
	}
	if logB := mustGit(t, pathB, "log", "--oneline"); strings.Contains(logB, "work on A") {
		t.Fatalf("run A's commit appears in run B's branch log:\n%s", logB)
	}
	// And A genuinely has it (so the negative above isn't vacuous).
	if logA := mustGit(t, pathA, "log", "--oneline"); !strings.Contains(logA, "work on A") {
		t.Fatalf("run A's own commit missing from its log:\n%s", logA)
	}
}

// TestTeardownWorktree_NoNetGrowth (AC#3): after teardown the repo's worktree list is
// back to baseline — the entry is removed AND pruned.
func TestTeardownWorktree_NoNetGrowth(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	store := filepath.Join(t.TempDir(), "store")
	runID := "01RUNCCCCCCCCCCCCCCCCCCCCC"

	baseline := worktreeList(t, repo)

	path, err := provisionWorktree(ctx, repo, "", store, runID)
	if err != nil {
		t.Fatalf("provisionWorktree: %v", err)
	}
	if got := worktreeList(t, repo); len(got) != len(baseline)+1 {
		t.Fatalf("after provision, worktree list = %v; want baseline+1 (%d)", got, len(baseline)+1)
	}

	if err := teardownWorktree(ctx, repo, path); err != nil {
		t.Fatalf("teardownWorktree: %v", err)
	}

	// PROPERTY: no net growth — back to the baseline count, and the provisioned path is
	// gone from the list (removed AND pruned, not an orphaned admin entry).
	if got := worktreeList(t, repo); len(got) != len(baseline) {
		t.Fatalf("after teardown, worktree list = %v; want baseline (%d) — entry not removed+pruned", got, len(baseline))
	}
	if got := worktreeList(t, repo); resolvedContains(got, path) {
		t.Fatalf("torn-down worktree %q still appears in worktree list %v", path, got)
	}
}

// TestTeardownWorktree_PrunesOrphanedEntry (AC#3 fake-pass guard): an entry whose
// worktree DIR was deleted out from under git still appears in `git worktree list` until
// pruned. teardownWorktree must prune it (remove may fail; prune cleans the admin entry).
func TestTeardownWorktree_PrunesOrphanedEntry(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	store := filepath.Join(t.TempDir(), "store")
	runID := "01RUNDDDDDDDDDDDDDDDDDDDDD"

	baseline := worktreeList(t, repo)
	path, err := provisionWorktree(ctx, repo, "", store, runID)
	if err != nil {
		t.Fatalf("provisionWorktree: %v", err)
	}
	// Simulate the OS deleting the worktree dir without telling git: the admin entry
	// lingers in `git worktree list` until a prune.
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("rm worktree dir: %v", err)
	}
	// The orphaned entry is still listed (this is the guard the AC names).
	if got := worktreeList(t, repo); !resolvedContains(got, path) && len(got) == len(baseline) {
		t.Skip("git auto-pruned the orphaned entry; nothing to assert (still safe)")
	}

	if err := teardownWorktree(ctx, repo, path); err != nil {
		// remove may fail (dir gone) but prune must succeed; teardown is best-effort, so a
		// returned error is tolerable ONLY if the list is nonetheless back to baseline.
		t.Logf("teardownWorktree returned (best-effort): %v", err)
	}
	if got := worktreeList(t, repo); len(got) != len(baseline) {
		t.Fatalf("after teardown of an orphaned entry, worktree list = %v; want baseline (%d) — prune did not run", got, len(baseline))
	}
}

// TestTeardownWorktree_RepoLessNoop (preserve today's behaviour): a repo-less run
// (empty repo/path) tears down nothing and never errors.
func TestTeardownWorktree_RepoLessNoop(t *testing.T) {
	if err := teardownWorktree(context.Background(), "", ""); err != nil {
		t.Fatalf("repo-less teardown must be a no-op; got %v", err)
	}
}
