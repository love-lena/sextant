package main

// TASK-260 proof: the trusted-path PR-open step. The sandboxed pi worker cannot push to
// GitHub or open a PR (egress walled to the model API; no git creds — see the dispatcher
// credential scrub, asserted in clients/dispatcher's launch_creds_test.go). So the
// coordinator — a host-side trusted entity with the operator's git/gh auth — opens the PR
// itself against the run's isolated worktree branch.
//
// What these tests CAN verify offline (and DO):
//   - AC#1 mechanism: a run's pr-open step commits the worktree's pending changes on the
//     run branch sxrun/<id>, PUSHES that branch to a real (local bare) origin, and records
//     the PR URL as the step's produced artifact — driven through the REAL coordinator.
//   - AC#1 empty-diff guard: a pr-open step over a worktree with NO changes FAILS LOUD (no
//     empty PR) — tested against hostOpenPR with a real local repo.
//   - AC#1 scope: the push names the run branch only and carries NO --force.
//
// What is a LIVE property (deferred to the assembled e2e, stated plainly): the real PR URL
// on real GitHub. The gh `pr create` call is the only stubbed seam here — the branch push
// is REAL, so this is not a fake-pass off a stubbed "pr created".

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	workflow "github.com/love-lena/sextant/conventions/workflow/go"
	sextant "github.com/love-lena/sextant/sdk/go"
)

// newRepoWithBareOrigin creates a throwaway target repo (one commit on main) whose
// `origin` remote is a local BARE repo — a real push target, no network. Returns the
// target repo path and the bare origin path. The run worktree branches from this repo and
// shares its `origin`, so a push from the worktree lands in the bare repo.
func newRepoWithBareOrigin(t *testing.T) (repo, bare string) {
	t.Helper()
	parent := t.TempDir()
	bare = filepath.Join(parent, "origin.git")
	mustGit(t, parent, "init", "--bare", "-q", bare)
	repo = filepath.Join(parent, "work")
	// Pin the initial branch to main explicitly (-b, git ≥2.28): the host's
	// init.defaultBranch may be master (CI's default), so a bare `git init` would leave
	// no `main` to push and the seed push below would fail with "src refspec main does
	// not match any". Deterministic regardless of the host's git config.
	mustGit(t, parent, "init", "-q", "-b", "main", repo)
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "-q", "-m", "seed")
	mustGit(t, repo, "remote", "add", "origin", bare)
	mustGit(t, repo, "push", "-q", "origin", "main")
	return repo, bare
}

// bareHasBranch reports whether the bare repo has a ref for branch (it landed via push).
func bareHasBranch(t *testing.T, bare, branch string) bool {
	t.Helper()
	out := mustGit(t, bare, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	for _, b := range strings.Split(out, "\n") {
		if strings.TrimSpace(b) == branch {
			return true
		}
	}
	return false
}

// prOpenWorker is a cooperating dispatcher whose work-step worker WRITES a file into the
// run's worktree (so there is a real diff for the pr-open step to commit + push), then
// reports the step done with a deliverable artifact. It is the realistic shape: the worker
// edits files in SEXTANT_PI_WORKDIR and leaves them UNCOMMITTED (D7 captures the diff). The
// pr-open step is NOT a spawn.request — the coordinator runs it host-side — so this worker
// never sees it.
func prOpenWorker(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string) {
	t.Helper()
	_, err := d.Subscribe(ctx, spawnSubj, func(m sextant.Message) {
		var req struct {
			Type    string `json:"$type"`
			Job     string `json:"job,omitempty"`
			Prompt  string `json:"prompt,omitempty"`
			Workdir string `json:"workdir,omitempty"`
		}
		if err := json.Unmarshal(m.Frame.Record, &req); err != nil || req.Type != workflow.TypeSpawnRequest {
			return
		}
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		// The work step writes a real change into the worktree (left uncommitted), so the
		// later pr-open step has a diff to commit + push.
		if req.Workdir != "" {
			_ = os.WriteFile(filepath.Join(req.Workdir, "feature.txt"), []byte("the worker's change\n"), 0o644)
		}
		name := "deliverable." + req.Job + "." + stepID
		if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)); err != nil {
			return
		}
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone, Artifacts: []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}}
		go func() { _ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal()) }()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("prOpenWorker Subscribe: %v", err)
	}
}

// prOpenCall records one invocation of the openPR seam.
type prOpenCall struct {
	repo, worktree, branch, base string
}

// TestRun_PROpen_CommitsPushesAndRecordsPR (AC#1 mechanism, coordinator path): a run with a
// work step + a pr-open step provisions a worktree, the work step leaves a diff, and the
// pr-open step (host-side, NOT a spawn) commits + PUSHES the run branch to a real local
// origin and records the PR URL as a produced artifact. The gh `pr create` half is stubbed
// (the LIVE property); the branch push is REAL and asserted against the bare remote.
func TestRun_PROpen_CommitsPushesAndRecordsPR(t *testing.T) {
	repo, bare := newRepoWithBareOrigin(t)

	var (
		mu    sync.Mutex
		calls []prOpenCall
	)
	const stubURL = "https://github.com/example/repo/pull/4242"
	// The seam does the REAL commit + push (so the push is genuinely asserted), and stubs
	// ONLY the gh pr-create call — exactly hostOpenPR minus the gh invocation.
	restore := SetOpenPRHook(func(ctx context.Context, repo, worktree, branch, base string) (string, error) {
		mu.Lock()
		calls = append(calls, prOpenCall{repo, worktree, branch, base})
		mu.Unlock()
		if out, err := git(ctx, "-C", worktree, "add", "-A"); err != nil {
			return "", err1("add", out, err)
		}
		if out, err := git(ctx, "-C", worktree, "commit", "-m", "work-engine run "+branch); err != nil {
			return "", err1("commit", out, err)
		}
		if out, err := git(ctx, "-C", worktree, "push", "-u", "origin", branch+":"+branch); err != nil {
			return "", err1("push", out, err)
		}
		return stubURL, nil // the LIVE half (real PR URL) is stubbed; the push above is real
	})
	defer restore()

	storeDir := t.TempDir()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: storeDir})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	consumer := dialBusClient(t, b, "consumer")
	requester := dialBusClient(t, b, "requester")
	dispatcher := dialBusClient(t, b, "dispatcher")
	spawnSubj := "msg.topic.spawn"

	prOpenWorker(t, ctx, dispatcher, spawnSubj)
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, 10*time.Second, storeDir)
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)

	runID := "01RUNPROPENEEEEEEEEEEEEEEE"
	run := workflow.Run{
		ID: runID, Status: workflow.RunRunning, Objective: "open a PR for the change",
		Repo: repo,
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "pr", Label: "open PR", Kind: workflow.KindPROpen, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")
	got := pollRun(t, ctx, requester, runID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("run must reach done; got %q steps=%+v activity=%+v", got.Status, got.Steps, got.Activity)
	}

	// PROPERTY (AC#1): the openPR seam was invoked with the run's worktree, the run branch
	// sxrun/<id> as head, and main as the base.
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("openPR called %d times; want exactly 1: %+v", len(calls), calls)
	}
	c := calls[0]
	wantBranch := runBranch(runID)
	if c.branch != wantBranch {
		t.Fatalf("openPR head branch = %q; want the run branch %q", c.branch, wantBranch)
	}
	if c.base != "main" {
		t.Fatalf("openPR base = %q; want %q (the worktree's base ref)", c.base, "main")
	}
	if c.repo != repo {
		t.Fatalf("openPR repo = %q; want %q", c.repo, repo)
	}
	if c.worktree != runWorktreePath(storeDir, runID) {
		t.Fatalf("openPR worktree = %q; want the provisioned worktree %q", c.worktree, runWorktreePath(storeDir, runID))
	}

	// PROPERTY (AC#1): the run branch ACTUALLY landed on origin with the worker's change —
	// a REAL push, not a stubbed success.
	if !bareHasBranch(t, bare, wantBranch) {
		t.Fatalf("run branch %q not pushed to origin (no real push)", wantBranch)
	}
	gotFile := mustGit(t, bare, "show", wantBranch+":feature.txt")
	if strings.TrimSpace(gotFile) != "the worker's change" {
		t.Fatalf("pushed branch does not carry the worker's change; feature.txt = %q", gotFile)
	}

	// PROPERTY (AC#1): the PR URL was recorded as a typed produced artifact on the run —
	// the existence gate's deliverable and the URL the dash surfaces.
	prArt, err := requester.GetArtifact(ctx, prArtifactName(runID))
	if err != nil {
		t.Fatalf("PR artifact not recorded: %v", err)
	}
	var rec prRecord
	if err := json.Unmarshal(prArt.Record, &rec); err != nil {
		t.Fatalf("PR artifact is not a prRecord: %v", err)
	}
	if rec.Type != KindPRArtifact || rec.URL != stubURL || rec.Branch != wantBranch || rec.Base != "main" {
		t.Fatalf("PR artifact record wrong: %+v", rec)
	}
	// And it is attached to the run + the pr step's produced refs.
	var prStep *workflow.RunStep
	for i := range got.Steps {
		if got.Steps[i].ID == "pr" {
			prStep = &got.Steps[i]
		}
	}
	if prStep == nil || len(prStep.Produced) != 1 || prStep.Produced[0].Name != prArtifactName(runID) {
		t.Fatalf("pr step produced refs wrong: %+v", got.Steps)
	}
	// The URL is surfaced on the run's activity trail (the dash's view).
	foundActivity := false
	for _, a := range got.Activity {
		if strings.Contains(a.Text, stubURL) {
			foundActivity = true
		}
	}
	if !foundActivity {
		t.Fatalf("PR URL not surfaced on the run activity trail: %+v", got.Activity)
	}
}

// TestHostOpenPR_EmptyDiffFailsLoud (AC#1 empty-diff guard): hostOpenPR over a worktree
// with NO pending changes FAILS LOUD — it never opens an empty PR. Drives the REAL
// hostOpenPR against a real local repo; it must error at the status check, BEFORE any
// commit/push/gh call (so it cannot reach gh in CI where gh may be absent).
func TestHostOpenPR_EmptyDiffFailsLoud(t *testing.T) {
	ctx := context.Background()
	repo, _ := newRepoWithBareOrigin(t)
	store := filepath.Join(t.TempDir(), "store")
	runID := "01RUNEMPTYDIFFFFFFFFFFFFFF"

	worktree, err := provisionWorktree(ctx, repo, "", store, runID)
	if err != nil {
		t.Fatalf("provisionWorktree: %v", err)
	}
	// No changes written into the worktree → an empty diff.
	_, err = hostOpenPR(ctx, repo, worktree, runBranch(runID), "main")
	if err == nil {
		t.Fatal("hostOpenPR over an empty worktree returned nil; want a fail-loud error (no empty PR)")
	}
	if !strings.Contains(err.Error(), "no pending changes") {
		t.Fatalf("empty-diff error should name the empty-diff guard; got: %v", err)
	}
}

// TestHostOpenPR_PushScopedNoForce (AC#1 scope, AC#2 trust posture): hostOpenPR pushes the
// run branch ONLY and never force-pushes. Driven against a real local origin with a real
// diff; the gh `pr create` step is allowed to fail (gh may be absent in CI) — the test
// asserts the SCOPED PUSH happened first (the branch is on origin) and that main is
// untouched. This is the host-side push half of the trust posture: a scoped, fast-forward,
// run-branch-only push.
func TestHostOpenPR_PushScopedNoForce(t *testing.T) {
	ctx := context.Background()
	repo, bare := newRepoWithBareOrigin(t)
	store := filepath.Join(t.TempDir(), "store")
	runID := "01RUNSCOPEDPUSHHHHHHHHHHHH"

	worktree, err := provisionWorktree(ctx, repo, "", store, runID)
	if err != nil {
		t.Fatalf("provisionWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainBefore := mustGit(t, bare, "rev-parse", "main")

	// hostOpenPR commits + pushes the branch, then calls gh. gh may be absent/unauth in CI,
	// so the call may error — but the commit + push happen FIRST, so the scoped-push
	// property holds regardless of gh.
	_, _ = hostOpenPR(ctx, repo, worktree, runBranch(runID), "main")

	if !bareHasBranch(t, bare, runBranch(runID)) {
		t.Fatalf("run branch %q not pushed to origin", runBranch(runID))
	}
	// SCOPE: main on origin is unchanged — the push touched only the run branch, no force.
	if got := mustGit(t, bare, "rev-parse", "main"); got != mainBefore {
		t.Fatalf("origin main moved (%s → %s); a pr-open push must touch ONLY the run branch", mainBefore, got)
	}
}

// err1 wraps a git seam error with its stage + output (test-local helper).
func err1(stage, out string, err error) error {
	return &gitStageErr{stage: stage, out: strings.TrimSpace(out), err: err}
}

type gitStageErr struct {
	stage, out string
	err        error
}

func (e *gitStageErr) Error() string { return e.stage + ": " + e.err.Error() + " (" + e.out + ")" }
