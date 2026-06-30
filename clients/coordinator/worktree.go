package main

import (
	"context"
	"fmt"
	"os"
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

	// Ignore the sandbox runtime's CWD scratch (D15). The pi worker runs with its
	// CWD set to this worktree, and the srt sandbox + pi runtime write scratch files
	// into it (the recipe's clients/dispatcher/recipes/pi.sh writes
	// `${WORKDIR}/.sx-srt-settings.json` and creates `${WORKDIR}/.pi-agent/`, into
	// which pi drops an `auth.json`). Without ignoring them, D7's worktree-diff
	// capture sees them as untracked changes and pr.go's `git add -A` sweeps them
	// into the run's commit — every work-engine PR then carried these stray env
	// artifacts alongside the real deliverable (PR #322). We exclude them LOCALLY and
	// PER-WORKTREE (never a committed .gitignore — these are environment artifacts,
	// not a repo concern; and never the operator's primary checkout), so
	// `git status`/`diff`/`add` in THIS worktree skip them while every other worktree
	// is unaffected. Best-effort, like teardown: a failed exclude write is logged
	// (never fatal) and provisioning still succeeds (a hygiene improvement, not a
	// precondition).
	if err := writeWorktreeExcludes(ctx, path); err != nil {
		logf("warn: write per-worktree sandbox-scratch excludes for %s: %v", path, err)
	}
	return path, nil
}

// sandboxScratchExcludes are the CWD-rooted scratch paths the srt sandbox + pi
// runtime write into the worker's worktree (see clients/dispatcher/recipes/pi.sh:
// the `.sx-srt-settings.json` srt profile and the `.pi-agent/` scoped pi config dir
// it creates, into which pi writes an `auth.json`). They are environment artifacts,
// never part of the run's deliverable, so we git-ignore them per-worktree (D15). The
// session JSONL and per-child creds live OUTSIDE the workdir (siblings of the store),
// so they never reach the worktree and need no exclude.
var sandboxScratchExcludes = []string{
	".pi-agent/",
	".sx-srt-settings.json",
}

// writeWorktreeExcludes points THIS worktree (and only this worktree) at a local
// exclude file listing the sandbox scratch patterns, so they are neither captured by
// D7's worktree-diff nor committed by pr.go's `git add -A`, while every other worktree
// — the operator's primary checkout above all — is untouched.
//
// We use git's per-worktree config, which is the only mechanism that gives a TRUE
// per-worktree ignore (the obvious-looking `.git/worktrees/<name>/info/exclude` is
// NOT honoured by git, and `rev-parse --git-path info/exclude` resolves to the SHARED
// common-dir `info/exclude`, which would leak the ignore into the operator's primary
// checkout — verified, not assumed):
//   - turn on `extensions.worktreeConfig` (idempotent, repo-wide flag — inert for
//     existing worktrees, which keep reading the shared config);
//   - write the patterns to a file inside this worktree's OWN gitdir
//     (`<gitdir>/info/sx-exclude`, gitdir = `--git-dir`, the per-worktree
//     `.git/worktrees/<id>` dir);
//   - set `core.excludesFile` to that file in the WORKTREE config scope
//     (`git config --worktree`), so only this worktree reads it.
//
// BEST-EFFORT and panic-free (mirrors teardownWorktree's style): on any failure it
// returns the error for the caller to log, never raises — provisioning must still
// succeed even if the ignore write fails (the diff would merely carry the scratch
// again, the pre-D15 behaviour, not break the run).
func writeWorktreeExcludes(ctx context.Context, worktree string) error {
	// This worktree's OWN gitdir (the linked worktree's .git/worktrees/<id> dir), where
	// the worktree-local exclude file lives. `--git-dir` is per-worktree (unlike
	// `--git-path info/exclude`, which resolves to the SHARED common-dir exclude).
	out, err := git(ctx, "-C", worktree, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("writeWorktreeExcludes: resolve git dir for %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	}
	gitDir := strings.TrimSpace(out)
	if gitDir == "" {
		return fmt.Errorf("writeWorktreeExcludes: empty git dir for %s", worktree)
	}

	// Enable per-worktree config (idempotent; harmless to existing worktrees, which
	// keep reading the shared config). Required before `git config --worktree` will
	// scope a key to one worktree rather than the whole repo.
	if out, err := git(ctx, "-C", worktree, "config", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("writeWorktreeExcludes: enable worktreeConfig for %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	}

	// Write the patterns to a worktree-local exclude file inside this worktree's gitdir.
	excludePath := filepath.Join(gitDir, "info", "sx-exclude")
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("writeWorktreeExcludes: mkdir %s: %w", filepath.Dir(excludePath), err)
	}
	body := strings.Join(sandboxScratchExcludes, "\n") + "\n"
	if err := os.WriteFile(excludePath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writeWorktreeExcludes: write %s: %w", excludePath, err)
	}

	// Point ONLY this worktree at that exclude file (worktree config scope).
	if out, err := git(ctx, "-C", worktree, "config", "--worktree", "core.excludesFile", excludePath); err != nil {
		return fmt.Errorf("writeWorktreeExcludes: set worktree core.excludesFile for %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	}
	return nil
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
