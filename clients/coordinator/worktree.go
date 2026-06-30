package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Per-run isolated git worktree provisioning (TASK-256). A run that declares a
// target repo (Run.Repo) gets ONE git worktree for its whole lifetime: the
// coordinator provisions it at adopt, threads its path to every step's worker via
// SpawnRequest.Workdir → SEXTANT_PI_WORKDIR, and tears it down when the run goes
// terminal. One worktree per run (not per step) so step N sees step N-1's changes —
// the run accumulates its diff on one branch.
//
// SAFETY (the ticket's contamination risk). Provisioned worktrees live ONLY under a
// dedicated scratch dir (a SIBLING of the bus store, never the target repo's own
// tree and never an existing checkout), each on its own run-namespaced branch
// sxrun/<runID>. Nothing here mutates the operator's primary checkout or any
// existing sibling worktree: `git worktree add <newdir>` only CREATES a fresh linked
// worktree; it never touches the main working tree or other linked worktrees. The
// repo path comes from the RUN definition (Run.Repo), never an operator-set env var.

// worktreeScratchDir is the directory under which every per-run worktree is created
// — a sibling of the bus store (its parent's run-worktrees/), the same neighbourhood
// the recipe's per-child scratch dirs live in. Never the target repo's own tree.
func worktreeScratchDir(store string) string {
	return filepath.Join(filepath.Dir(store), "run-worktrees")
}

// runBranch is the run-namespaced branch a per-run worktree checks out. Namespacing
// by run id guarantees two runs against the same repo get DISTINCT branches (AC#2):
// neither can see the other's commits.
func runBranch(runID string) string { return "sxrun/" + runID }

// runWorktreePath is the per-run worktree path: scratch/run-worktrees/<runID>.
// Distinct per run id, so two concurrent runs never share a path (AC#2).
func runWorktreePath(store, runID string) string {
	return filepath.Join(worktreeScratchDir(store), runID)
}

// provisionWorktree creates the run's isolated worktree: a fresh linked worktree of
// repo at scratch/run-worktrees/<runID> on branch sxrun/<runID>, branched from ref
// (or HEAD when ref is empty). It returns the worktree's absolute path. It is a
// REAL linked worktree (appears under `git worktree list`), not a scratch dir with
// its own `.git init` (AC#1's fake-pass guard). Idempotent on resume: if the path is
// already a registered worktree of repo, it is returned as-is (a resumed coordinator
// re-adopts the same run).
//
// repo must be an absolute path to an existing git repository; ref may be empty
// (HEAD). On any failure NO worktree is left behind that a later teardown wouldn't
// clean: git either creates the worktree or it does not.
func provisionWorktree(ctx context.Context, repo, ref, store, runID string) (string, error) {
	if repo == "" {
		return "", fmt.Errorf("provisionWorktree: empty repo (caller must guard)")
	}
	if !filepath.IsAbs(repo) {
		return "", fmt.Errorf("provisionWorktree: repo %q is not an absolute path (must come from the run definition)", repo)
	}
	if err := assertGitRepo(ctx, repo); err != nil {
		return "", err
	}
	path := runWorktreePath(store, runID)

	// Resume idempotency: if this exact path is already a worktree of repo, reuse it
	// rather than failing on `git worktree add` (a re-adopted run).
	if listed, err := worktreeRegistered(ctx, repo, path); err == nil && listed {
		return path, nil
	}

	branch := runBranch(runID)
	// `git worktree add -b <branch> <path> [ref]` creates the linked worktree AND the
	// branch atomically. It never touches the main working tree or other linked
	// worktrees (it only writes the new <path> and a pointer under repo/.git/worktrees).
	args := []string{"-C", repo, "worktree", "add", "-b", branch, path}
	if ref != "" {
		args = append(args, ref)
	}
	if out, err := git(ctx, args...); err != nil {
		return "", fmt.Errorf("provisionWorktree: git worktree add %s (branch %s) from %s: %w (%s)", path, branch, repo, err, strings.TrimSpace(out))
	}
	return path, nil
}

// teardownWorktree removes the run's worktree and prunes the repo's worktree list,
// leaving NO net growth in `git worktree list` (AC#3). BEST-EFFORT and panic-free: a
// failure is returned for the caller to log, never raised — a teardown that can't run
// must not crash a coordinator finishing a run. It always runs `prune` even if
// `remove` fails, so an entry the OS already deleted is still cleaned (AC#3's
// fake-pass guard: prune removes administrative entries whose worktree dir is gone).
func teardownWorktree(ctx context.Context, repo, path string) error {
	if repo == "" || path == "" {
		return nil // a repo-less run provisioned nothing
	}
	// A short bounded context so teardown on a cancelled run can't hang.
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	var firstErr error
	if out, err := git(tctx, "-C", repo, "worktree", "remove", "--force", path); err != nil {
		firstErr = fmt.Errorf("worktree remove %s: %w (%s)", path, err, strings.TrimSpace(out))
	}
	// Prune regardless — clears any administrative entry whose dir is already gone.
	if out, err := git(tctx, "-C", repo, "worktree", "prune"); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("worktree prune (%s): %w (%s)", repo, err, strings.TrimSpace(out))
	}
	return firstErr
}

// assertGitRepo confirms repo is inside a git work tree (a real repo), failing loud
// otherwise so provisioning never runs git operations against a non-repo path.
func assertGitRepo(ctx context.Context, repo string) error {
	if out, err := git(ctx, "-C", repo, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("provisionWorktree: %q is not a git repository: %w (%s)", repo, err, strings.TrimSpace(out))
	}
	return nil
}

// worktreeRegistered reports whether path is a registered linked worktree of repo
// (an entry under `git worktree list --porcelain`). Used for resume idempotency and
// as the proof seam AC#1/#3 assert against.
func worktreeRegistered(ctx context.Context, repo, path string) (bool, error) {
	out, err := git(ctx, "-C", repo, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("worktree list %s: %w (%s)", repo, err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		// Compare resolved paths: `git worktree list` may print a symlink-resolved
		// path (e.g. macOS /var → /private/var), so a literal string match would miss.
		if strings.HasPrefix(line, "worktree ") {
			listed := strings.TrimPrefix(line, "worktree ")
			if listed == path || sameFile(listed, path) {
				return true, nil
			}
		}
	}
	return false, nil
}

// sameFile reports whether two paths resolve to the same location, tolerating the
// symlink resolution `git worktree list` applies to its output.
func sameFile(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	return ra == rb
}

// git runs a git subcommand and returns its combined output. A thin seam so every
// worktree operation funnels through one place (and a test can read the real output).
func git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gh runs a gh subcommand from dir and returns its combined output — the thin host-side
// seam the trusted PR-open step (pr.go) uses to open a PR. It runs with the coordinator's
// host environment (the operator's gh auth), never inside the sandbox. cwd is set to the
// run's worktree so gh resolves the repo from its origin remote.
func gh(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
