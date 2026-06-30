package main

// TASK-256 end-to-end (coordinator path): a run that declares a target Repo provisions
// ONE per-run worktree at adopt, threads its path to EVERY step's worker via the
// spawn.request's Workdir field, and tears the worktree down when the run goes terminal.
// Driven through the REAL coordinator (newStartConsumer → adopt → walk → runDispatch →
// finish) over a real bus, with a cooperating test dispatcher that records the Workdir
// each spawn.request carried — the same shape the M5.2 dispatcher consumes.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/conventions/workflow/go"
	"github.com/love-lena/sextant/sdk/go"
)

// recordingDispatcher is cooperatingDispatcher plus: it records the Workdir of every
// spawn.request it sees, so the test can assert what the coordinator threaded.
func recordingDispatcher(t *testing.T, ctx context.Context, d *sextant.Client, spawnSubj string, seen *workdirLog) {
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
		seen.add(req.Workdir)
		ack := workflow.SpawnAck{Type: workflow.TypeSpawnAck, ID: "agent-" + m.Frame.ID[:8], RequestID: m.Frame.ID, Status: workflow.StatusOK}
		ackBytes, _ := json.Marshal(ack)
		if err := d.Publish(ctx, spawnSubj, json.RawMessage(ackBytes)); err != nil {
			return
		}
		stepID := parseDirective(req.Prompt, "RUN_STEP")
		ev := workflow.RunEvent{Step: stepID, Status: workflow.StepDone}
		if strings.Contains(req.Prompt, "stopping brief") {
			ev.Outcome = workflow.RunDone
			name := "brief.stopping." + req.Job
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"brief","outcome":"done"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "stopping", Version: 1}}
		} else {
			name := "deliverable." + req.Job + "." + stepID
			if _, err := d.CreateArtifact(ctx, name, json.RawMessage(`{"$type":"work","step":"`+stepID+`"}`)); err != nil {
				return
			}
			ev.Artifacts = []workflow.ProducedArtifact{{Name: name, Kind: "work", Version: 1}}
		}
		go func() { _ = d.Publish(ctx, workflow.RunEventsSubject(req.Job), ev.Marshal()) }()
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("recordingDispatcher Subscribe: %v", err)
	}
}

type workdirLog struct {
	mu   sync.Mutex
	dirs []string
}

func (l *workdirLog) add(d string) {
	l.mu.Lock()
	l.dirs = append(l.dirs, d)
	l.mu.Unlock()
}

func (l *workdirLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.dirs...)
}

// TestRun_ProvisionsAndThreadsWorktree (AC#1 through the coordinator path + AC#3): a run
// declaring Repo provisions a worktree, threads its path as Workdir to every step's
// spawn.request, and tears it down on terminal — no net growth in `git worktree list`.
func TestRun_ProvisionsAndThreadsWorktree(t *testing.T) {
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

	repo := newRepo(t)
	store := storeDir // the coordinator derives the worktree scratch from the store's parent
	baseline := worktreeList(t, repo)

	seen := &workdirLog{}
	recordingDispatcher(t, ctx, dispatcher, spawnSubj, seen)

	// Listen mode with a REAL store, so adopt provisions the run's worktree.
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, 10*time.Second, store)
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)

	run := workflow.Run{
		ID: "01RUNWORKTREEEEEEEEEEEEEEE", Status: workflow.RunRunning, Objective: "provision a worktree",
		Repo: repo,
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")
	got := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("run must reach done; got %q steps=%+v", got.Status, got.Steps)
	}

	wantPath := runWorktreePath(store, run.ID)

	// PROPERTY (AC#1, coordinator path): every spawn.request carried the run's worktree
	// path as Workdir — both the work step and the brief step ran inside it.
	dirs := seen.all()
	if len(dirs) == 0 {
		t.Fatalf("no spawn.request observed")
	}
	for i, d := range dirs {
		if d != wantPath {
			t.Fatalf("spawn.request[%d] Workdir = %q; want the provisioned worktree %q (every step shares one run worktree)", i, d, wantPath)
		}
	}

	// PROPERTY (AC#3): the worktree was torn down on terminal — no net growth in the
	// repo's worktree list. Allow a brief settle for finish()'s teardown.
	deadline := time.Now().Add(5 * time.Second)
	var list []string
	for time.Now().Before(deadline) {
		list = worktreeList(t, repo)
		if len(list) == len(baseline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(list) != len(baseline) {
		t.Fatalf("after the run finished, worktree list = %v; want baseline (%d) — teardown did not run+prune", list, len(baseline))
	}
	if resolvedContains(list, wantPath) {
		t.Fatalf("run worktree %q still in list after terminal: %v", wantPath, list)
	}
}

// TestRun_RepoLess_NoWorkdir (preserve today's behaviour): a run with no Repo provisions
// nothing — its spawn.requests carry an EMPTY Workdir, so the worker falls back to the
// recipe's scratch default. The fallback must not regress.
func TestRun_RepoLess_NoWorkdir(t *testing.T) {
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

	seen := &workdirLog{}
	recordingDispatcher(t, ctx, dispatcher, spawnSubj, seen)
	_, sub, err := newStartConsumer(ctx, consumer, spawnSubj, 10*time.Second, storeDir)
	if err != nil {
		t.Fatalf("newStartConsumer: %v", err)
	}
	t.Cleanup(sub.Stop)

	run := workflow.Run{
		ID: "01RUNNOREPOOOOOOOOOOOOOOOO", Status: workflow.RunRunning, Objective: "repo-less run",
		Steps: []workflow.RunStep{
			{ID: "s1", Label: "work", Kind: workflow.KindWork, Status: workflow.StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: workflow.KindBrief, Status: workflow.StepUpcoming},
		},
	}
	writeRunAndStart(t, ctx, requester, run, "")
	got := pollRun(t, ctx, requester, run.ID, 20*time.Second, func(r workflow.Run) bool {
		return workflow.IsTerminalRun(r.Status)
	})
	if got.Status != workflow.RunDone {
		t.Fatalf("repo-less run must reach done; got %q", got.Status)
	}
	for i, d := range seen.all() {
		if d != "" {
			t.Fatalf("repo-less spawn.request[%d] Workdir = %q; want empty (scratch-default fallback preserved)", i, d)
		}
	}
}
