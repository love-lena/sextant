package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	workflow "github.com/love-lena/sextant/conventions/workflow/go"
)

// Trusted-path PR-open step (TASK-260).
//
// TRUST POSTURE. The work engine's coding worker is a sandboxed pi process whose
// egress is walled to the model API only — github.com is DENIED (TASK-118, ADR-0052)
// — and which is never handed git/gh credentials (see the dispatcher's credential
// scrub in clients/dispatcher/main.go: launch). So the jailed worker physically
// CANNOT push a branch or open a PR; by design it only edits files in the run's
// isolated worktree, and the D7 reporter captures the uncommitted diff as the step's
// artifact.
//
// Turning that diff into a real PR therefore needs a TRUSTED host-side entity. The
// coordinator is exactly that: a managed Go service running on the operator's host
// (under launchd) with the operator's ambient git/gh authority, NOT inside the
// sandbox. A pr-open step runs HERE, in-process, against the run's own worktree —
// never as a spawn.request to the sandboxed dispatcher.
//
// What the trusted entity MAY do, scoped tightly:
//   - commit the worktree's pending changes on the run-namespaced branch sxrun/<id>;
//   - push THAT branch to origin (a fresh per-run branch, force-push DISABLED);
//   - open a PR from sxrun/<id> against the run's base ref.
//
// What it must NOT do: it never force-pushes, never pushes a shared/long-lived branch
// (main, the rc line), and never touches a branch other than this run's own. The push
// refspec is the literal run branch, and runPROpen passes no --force. The worker's
// credential-free jail and this scoped host-side push are the two halves of the
// posture: the credential boundary is enforced at the dispatcher (the worker's env
// carries no GH_TOKEN / SSH key); the scope boundary is enforced here (one run branch,
// no force).

// KindPRArtifact is the $type of the artifact a pr-open step produces — a typed record
// of the opened PR (its URL, branch, and base). It is a regular ProducedArtifact on the
// step, so the deterministic existence gate (verifyReportedArtifactsExist) treats the PR
// as a first-class deliverable: a pr-open step that reports done MUST have created this
// artifact on the bus, and the URL it carries is the real-PR proof AC#1 asks for. The
// dash surfaces the URL via the activity entry runPROpen appends.
const KindPRArtifact = "sextant.pr/v1"

// prArtifactName is the bus name of a run's PR artifact: workflow.run.<id>.pr, one per
// run (a run opens at most one PR). Namespaced under the run so it is discoverable
// alongside the run-state envelope.
func prArtifactName(runID string) string { return workflow.RunStateName(runID) + ".pr" }

// prRecord is the typed body of the PR artifact.
type prRecord struct {
	Type   string `json:"$type"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	RunID  string `json:"run_id"`
	At     int64  `json:"at"`
}

// openPR is the coordinator's PR-open SEAM (default: the real git/gh host-side path).
// It commits the worktree's pending changes on branch, pushes the branch to origin, and
// opens a PR against base — returning the PR URL. Made injectable (like worktree.go's
// git() helper) so runPROpen is testable against a LOCAL bare repo without a live
// GitHub: a test swaps in a seam that performs the real branch commit + push (so the
// push is genuinely asserted) and stubs only the gh `pr create` call (the LIVE half).
type openPRFunc func(ctx context.Context, repo, worktree, branch, base string) (string, error)

// runPROpen runs the trusted-path PR-open step (TASK-260) HOST-SIDE, in-process — never
// a spawn.request. It commits + pushes the run's worktree branch and opens a PR via the
// coordinator's openPR seam, then records the PR URL as the step's produced artifact so
// the existence gate passes and the URL is surfaced on the run's activity trail.
//
// FAIL-LOUD on an empty diff: a pr-open step over a worktree with NO pending changes does
// NOT open an empty PR — it returns an error so the run blocks (the operator sees a real
// reason, not a vacuous green PR). A repo-less run (no provisioned worktree) likewise
// fails: there is no branch to push.
func (co *coordinator) runPROpen(step *workflow.RunStep) (string, error) {
	if co.run.Repo == "" || co.workdir == "" {
		return "", fmt.Errorf("pr-open step %q: run has no provisioned worktree (set Run.Repo so TASK-256 provisions a branch to push)", step.ID)
	}
	branch := runBranch(co.run.ID)
	base := co.prBase()

	url, err := co.openPR(co.ctx, co.run.Repo, co.workdir, branch, base)
	if err != nil {
		return "", fmt.Errorf("pr-open step %q: %w", step.ID, err)
	}

	// Record the PR as a typed artifact (the deliverable the existence gate checks) and
	// thread it onto the step's produced refs + the run's artifact ledger.
	name := prArtifactName(co.run.ID)
	rec := prRecord{Type: KindPRArtifact, URL: url, Branch: branch, Base: base, RunID: co.run.ID, At: co.nowMs()}
	body, _ := json.Marshal(rec)
	if _, err := co.c.CreateArtifact(co.ctx, name, body); err != nil {
		return "", fmt.Errorf("pr-open step %q: record PR artifact %q: %w", step.ID, name, err)
	}
	ref := workflow.ProducedArtifact{Name: name, Kind: KindPRArtifact, Version: 1}
	step.Produced = append(step.Produced, ref)
	co.attachArtifacts([]workflow.ProducedArtifact{ref})
	co.appendActivity("⇡", fmt.Sprintf("opened PR for %s: %s", branch, url))
	return "", nil
}

// prBase is the base ref the PR targets. It is the run's declared RepoRef (the ref the
// worktree branched from), or "main" when none was declared — a run that pinned a base
// opens its PR against that same base, so the PR's diff is exactly the run's changes.
func (co *coordinator) prBase() string {
	if co.run.RepoRef != "" {
		return co.run.RepoRef
	}
	return "main"
}

// hostOpenPR is the production openPR seam: it commits the worktree's pending changes on
// branch, pushes the branch to origin (scoped, no force), and opens a PR via gh. It runs
// with the coordinator's host environment (the operator's git/gh auth), NOT in the
// sandbox. Bounded so a hung network op fails loud rather than wedging the run.
func hostOpenPR(ctx context.Context, repo, worktree, branch, base string) (string, error) {
	octx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// FAIL-LOUD empty-diff guard: refuse to open an empty PR. `git status --porcelain`
	// over the worktree is empty when there is nothing to commit (the worker produced no
	// change). We check BEFORE committing so the branch is never advanced for an empty run.
	if out, err := git(octx, "-C", worktree, "status", "--porcelain"); err != nil {
		return "", fmt.Errorf("git status %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	} else if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("worktree %s has no pending changes — refusing to open an empty PR (the work step produced no diff)", worktree)
	}

	// Commit the worktree's pending changes on its own branch. -A stages every change the
	// worker left (it edits files but does not commit — D7 captures the uncommitted diff).
	if out, err := git(octx, "-C", worktree, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add -A %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	}
	msg := fmt.Sprintf("work-engine run %s", branch)
	if out, err := git(octx, "-C", worktree, "commit", "-m", msg); err != nil {
		return "", fmt.Errorf("git commit %s: %w (%s)", worktree, err, strings.TrimSpace(out))
	}

	// Push THE RUN BRANCH ONLY to origin — no --force, an explicit refspec that names the
	// run-namespaced branch on both sides. -u sets upstream so a later op tracks it. This
	// is the scoped trusted push: it can only fast-forward this fresh per-run branch.
	refspec := branch + ":" + branch
	if out, err := git(octx, "-C", worktree, "push", "-u", "origin", refspec); err != nil {
		return "", fmt.Errorf("git push origin %s: %w (%s)", refspec, err, strings.TrimSpace(out))
	}

	// Open the PR via gh against the run's base. --head is the run branch; gh resolves the
	// repo from the worktree's origin remote. Returns the PR URL on stdout.
	out, err := gh(octx, worktree, "pr", "create", "--base", base, "--head", branch,
		"--title", msg, "--body", "Opened by the sextant work engine for "+branch+".")
	if err != nil {
		return "", fmt.Errorf("gh pr create --base %s --head %s: %w (%s)", base, branch, err, strings.TrimSpace(out))
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("gh pr create returned no PR URL (head %s, base %s)", branch, base)
	}
	return url, nil
}
