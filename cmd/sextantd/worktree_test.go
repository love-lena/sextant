package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// startDaemonHarnessWithWorktree spins up a tiny ephemeral git repo
// and re-saves sextantd.toml so the daemon's worktree runtime points
// at it. Returns the harness, the live config, and the repo + tree
// paths so the test can `git` against them directly.
func startDaemonHarnessWithWorktree(t *testing.T) (*daemonHarness, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}

	// We can't reuse startDaemonHarness directly because we need to
	// inject the worktree config *before* the daemon binary starts.
	// We replicate the relevant prep here.
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Set up the ephemeral repo + worktrees root. macOS Unix socket
	// limits don't apply here (git worktree paths aren't sockets), but
	// we still keep them short by using os.MkdirTemp under /tmp.
	repoDir, err := os.MkdirTemp("", "sxt-repo")
	if err != nil {
		t.Fatalf("MkdirTemp repo: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repoDir) })

	worktreesDir, err := os.MkdirTemp("", "sxt-wts")
	if err != nil {
		t.Fatalf("MkdirTemp worktrees: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(worktreesDir) })

	// Seed the repo with one commit on main.
	runOrFailDaemon(t, repoDir, "git", "init", "-b", "main")
	runOrFailDaemon(t, repoDir, "git", "config", "user.email", "test@example.com")
	runOrFailDaemon(t, repoDir, "git", "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOrFailDaemon(t, repoDir, "git", "add", "README.md")
	runOrFailDaemon(t, repoDir, "git", "commit", "-m", "initial")

	cfg.Worktree.RepoRoot = repoDir
	cfg.Worktree.WorktreesRoot = worktreesDir
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Now boot the daemon using the same flow startDaemonHarness uses,
	// extracted into bootDaemonAtConfig.
	h := bootDaemonAtConfig(t, cfgPath)

	// Cleanup: garbage-collect any worktrees git knows about so the
	// .git/worktrees/<name> bookkeeping doesn't leak across the test's
	// repoDir teardown.
	t.Cleanup(func() {
		entries, _ := os.ReadDir(worktreesDir)
		for _, e := range entries {
			path := filepath.Join(worktreesDir, e.Name())
			cmd := exec.Command("git", "worktree", "remove", "--force", path) //nolint:gosec // test-controlled args
			cmd.Dir = repoDir
			_ = cmd.Run()
		}
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = repoDir
		_ = cmd.Run()
	})

	return h, repoDir, worktreesDir
}

// bootDaemonAtConfig is the daemon-bring-up tail of
// startDaemonHarness, factored out so we can stamp the config before
// boot. Keep in sync with the sister helper.
func bootDaemonAtConfig(t *testing.T, cfgPath string) *daemonHarness {
	t.Helper()
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextantd")
	build := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant-initial/cmd/sextantd") //nolint:gosec // test-controlled args
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextantd: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	logFile, err := os.CreateTemp(binDir, "sextantd.log")
	if err != nil {
		cancel()
		t.Fatalf("temp log: %v", err)
	}

	cmd := exec.Command(binPath, "--config", cfgPath) //nolint:gosec // test-controlled args
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("start daemon: %v", err)
	}

	h := &daemonHarness{cfg: cfg, cmd: cmd, logFile: logFile, ctx: ctx, cancel: cancel}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = logFile.Close()
		cancel()
	})

	greeting, err := waitForGreeting(ctx, cfg.Daemon.ControlSocket, 75*time.Second)
	if err != nil {
		t.Fatalf("greeting: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !strings.HasPrefix(greeting, "OK ") {
		t.Fatalf("greeting = %q, want OK prefix", greeting)
	}
	return h
}

func runOrFailDaemon(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // test-controlled args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func runCaptureDaemon(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // test-controlled args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestM14WorktreeAcceptance is the M14 acceptance test: an
// (operator-or-agent) caller invokes worktree_create, writes code in
// the worktree, commits it, calls worktree_merge, and the changes
// land on main.
//
// Wire path: CLI → operator NATS → sextantd RPC → handlers.WorktreeCreate
// → pkg/worktree.Manager → git worktree add. Same path the MCP tools
// take (the MCP tools dispatch through runRPCAsTool against the same
// handlers).
func TestM14WorktreeAcceptance(t *testing.T) {
	h, repoDir, worktreesDir := startDaemonHarnessWithWorktree(t)
	cli := rpcClient(t, h)
	ctx := context.Background()

	// 1. worktree_create
	var createResp sextantproto.WorktreeCreateResponse
	createCtx, createCancel := context.WithTimeout(ctx, 30*time.Second)
	defer createCancel()
	if err := cli.RPC(createCtx, rpc.VerbWorktreeCreate,
		sextantproto.WorktreeCreateRequest{Name: "feat-acceptance-001", BaseBranch: "main"},
		&createResp); err != nil {
		t.Fatalf("worktree_create: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	wt := createResp.Worktree
	if wt.Path == "" || wt.Branch != "feat-acceptance-001" {
		t.Fatalf("WorktreeCreate response = %+v", wt)
	}
	if filepath.Dir(wt.Path) != worktreesDir {
		t.Errorf("worktree path %q not under WorktreesRoot %q", wt.Path, worktreesDir)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree path: %v", err)
	}

	// 2. worktree_list shows it.
	var listResp sextantproto.WorktreeListResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &listResp); err != nil {
		t.Fatalf("worktree_list: %v", err)
	}
	foundActive := false
	for _, w := range listResp.Worktrees {
		if w.Name == "feat-acceptance-001" && w.Status == sextantproto.WorktreeStatusActive {
			foundActive = true
		}
	}
	if !foundActive {
		t.Errorf("worktree_list missing active feat-acceptance-001: %+v", listResp.Worktrees)
	}

	// 3. Write a file in the worktree + commit it. Using the host's
	// git for the commit since the test is the operator-driven path.
	helloPath := filepath.Join(wt.Path, "hello.txt")
	if err := os.WriteFile(helloPath, []byte("hello from worktree\n"), 0o600); err != nil {
		t.Fatalf("write hello.txt: %v", err)
	}
	// Worktree inherits the repo's git config; runOrFail will use the
	// repoDir's identity since we set it in startDaemonHarnessWithWorktree.
	runOrFailDaemon(t, wt.Path, "git", "config", "user.email", "test@example.com")
	runOrFailDaemon(t, wt.Path, "git", "config", "user.name", "tester")
	runOrFailDaemon(t, wt.Path, "git", "add", "hello.txt")
	runOrFailDaemon(t, wt.Path, "git", "commit", "-m", "add hello via worktree")

	// 4. worktree_diff returns content.
	var diffResp sextantproto.WorktreeDiffResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeDiff,
		sextantproto.WorktreeDiffRequest{Name: "feat-acceptance-001", Against: "main"},
		&diffResp); err != nil {
		t.Fatalf("worktree_diff: %v", err)
	}
	if !strings.Contains(diffResp.Diff, "hello.txt") || !strings.Contains(diffResp.Diff, "+hello from worktree") {
		t.Errorf("diff missing expected content: %q", diffResp.Diff)
	}

	// 5. worktree_merge into main.
	var mergeResp sextantproto.WorktreeMergeResponse
	mergeCtx, mergeCancel := context.WithTimeout(ctx, 30*time.Second)
	defer mergeCancel()
	if err := cli.RPC(mergeCtx, rpc.VerbWorktreeMerge,
		sextantproto.WorktreeMergeRequest{Name: "feat-acceptance-001", Target: "main"},
		&mergeResp); err != nil {
		t.Fatalf("worktree_merge: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !mergeResp.OK {
		t.Fatalf("merge returned OK=false; conflicts=%v", mergeResp.Conflicts)
	}

	// 6. hello.txt is now on main.
	mainHello := runCaptureDaemon(t, repoDir, "git", "show", "main:hello.txt")
	if mainHello != "hello from worktree" {
		t.Errorf("main:hello.txt = %q, want %q", mainHello, "hello from worktree")
	}

	// 7. KV status is now `merged`.
	var listAfter sextantproto.WorktreeListResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeList, sextantproto.WorktreeListRequest{}, &listAfter); err != nil {
		t.Fatalf("worktree_list post-merge: %v", err)
	}
	var post sextantproto.WorktreeInfo
	for _, w := range listAfter.Worktrees {
		if w.Name == "feat-acceptance-001" {
			post = w
		}
	}
	if post.Status != sextantproto.WorktreeStatusMerged {
		t.Errorf("post-merge status = %s, want merged", post.Status)
	}

	// 8. worktree_destroy (force=false works because status == merged).
	var destroyResp sextantproto.WorktreeDestroyResponse
	if err := cli.RPC(ctx, rpc.VerbWorktreeDestroy,
		sextantproto.WorktreeDestroyRequest{Name: "feat-acceptance-001"},
		&destroyResp); err != nil {
		t.Fatalf("worktree_destroy: %v", err)
	}
	if !destroyResp.OK {
		t.Errorf("destroy OK=false")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree path still exists post-destroy: %v", err)
	}

	// 9. No leftover .merge-* dirs.
	entries, _ := os.ReadDir(worktreesDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".merge-") {
			t.Errorf("stale merge worktree leftover: %s", e.Name())
		}
	}
}
