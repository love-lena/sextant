package worktree_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/worktree"
)

// fakeKV is an in-memory implementation of worktree.RegistryKV +
// worktree.LockKV. The same struct backs both buckets in tests —
// they exercise different keysets so collision isn't an issue.
type fakeKV struct {
	mu      sync.Mutex
	entries map[string][]byte
}

func newFakeKV() *fakeKV {
	return &fakeKV{entries: map[string][]byte{}}
}

func (f *fakeKV) Get(_ context.Context, key string) (jetstream.KeyValueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.entries[key]
	if !ok {
		return nil, jetstream.ErrKeyNotFound
	}
	return fakeEntry{key: key, value: v}, nil
}

func (f *fakeKV) Put(_ context.Context, key string, value []byte) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[key] = append([]byte(nil), value...)
	return uint64(len(f.entries)), nil
}

func (f *fakeKV) Create(_ context.Context, key string, value []byte) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entries[key]; ok {
		return 0, jetstream.ErrKeyExists
	}
	f.entries[key] = append([]byte(nil), value...)
	return uint64(len(f.entries)), nil
}

func (f *fakeKV) Delete(_ context.Context, key string, _ ...jetstream.KVDeleteOpt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
	return nil
}

func (f *fakeKV) ListKeys(_ context.Context, _ ...jetstream.WatchOpt) (jetstream.KeyLister, error) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.entries))
	for k := range f.entries {
		keys = append(keys, k)
	}
	f.mu.Unlock()
	ch := make(chan string, len(keys))
	for _, k := range keys {
		ch <- k
	}
	close(ch)
	return fakeLister{ch: ch}, nil
}

type fakeLister struct{ ch chan string }

func (l fakeLister) Keys() <-chan string { return l.ch }
func (l fakeLister) Stop() error         { return nil }

type fakeEntry struct {
	key   string
	value []byte
}

func (e fakeEntry) Bucket() string                  { return "" }
func (e fakeEntry) Key() string                     { return e.key }
func (e fakeEntry) Value() []byte                   { return e.value }
func (e fakeEntry) Revision() uint64                { return 1 }
func (e fakeEntry) Created() time.Time              { return time.Time{} }
func (e fakeEntry) Delta() uint64                   { return 0 }
func (e fakeEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValuePut }

// initRepo creates a tiny git repo with one commit on `main` and
// returns the repo path. t.Cleanup removes it.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	dir := t.TempDir()
	runOrFail(t, dir, "git", "init", "-b", "main")
	runOrFail(t, dir, "git", "config", "user.email", "test@example.com")
	runOrFail(t, dir, "git", "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, dir, "git", "add", "README.md")
	runOrFail(t, dir, "git", "commit", "-m", "initial")
	return dir
}

func runOrFail(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // test-controlled args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func runCapture(t *testing.T, dir, name string, args ...string) string {
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

// buildManager wires a Manager against a fresh repo. Returns the
// manager, the registry KV, and the locks KV so tests can poke at
// them. WorktreesRoot is a fresh tempdir per call; it's cleaned up
// via t.Cleanup.
//
// We also stage cleanup that calls `git worktree remove --force` on
// every leftover worktree dir under the root so the test doesn't
// stamp stale .git/worktrees/ bookkeeping into the OS tempdir
// (which would survive a `t.TempDir()` cleanup since the bookkeeping
// lives inside the repo, not the tempdir).
func buildManager(t *testing.T) (*worktree.Manager, *fakeKV, *fakeKV, string) {
	t.Helper()
	repo := initRepo(t)
	worktreesRoot := t.TempDir()
	reg := newFakeKV()
	locks := newFakeKV()
	m, err := worktree.New(worktree.Config{
		RepoRoot:      repo,
		WorktreesRoot: worktreesRoot,
		Registry:      reg,
		Locks:         locks,
		HolderID:      "test-holder",
		// TTL needs to comfortably exceed two serial git-merge
		// operations on a cold cache (TestMergeIsSerializedByLock
		// waits up to one TTL for the second merge to acquire).
		MergeLockTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		// Garbage-collect every worktree pointer git knows about so
		// the test's repo dir teardown doesn't leak .git/worktrees/
		// entries.
		entries, _ := os.ReadDir(worktreesRoot)
		for _, e := range entries {
			path := filepath.Join(worktreesRoot, e.Name())
			cmd := exec.Command("git", "worktree", "remove", "--force", path) //nolint:gosec // test-controlled args
			cmd.Dir = repo
			_ = cmd.Run()
		}
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = repo
		_ = cmd.Run()
	})
	return m, reg, locks, repo
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"feat-bus-routing-001", true},
		{"fix-clickhouse-migration-003", true},
		{"spec-nats-component-001", true},
		{"feat-x-001", true},
		{"feat-001", false},                   // missing desc
		{"feature-bus-routing-001", false},    // wrong kind
		{"feat-Bus-Routing-001", false},       // uppercase
		{"feat-bus-routing-1", false},         // seq not 3 digits
		{"feat-bus-routing-0001", false},      // seq 4 digits
		{"FEAT-bus-routing-001", false},       // uppercase kind
		{"feat-bus-routing-001-extra", false}, // trailing
	}
	for _, c := range cases {
		err := worktree.ValidateName(c.name)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v, err=%v", c.name, c.ok, err)
		}
	}
}

func TestCreateListDiffDestroyRoundTrip(t *testing.T) {
	m, reg, _, repo := buildManager(t)
	ctx := context.Background()

	info, err := m.Create(ctx, "feat-hello-world-001", "main", uuid.New())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.Branch != "feat-hello-world-001" || info.BaseBranch != "main" {
		t.Errorf("Info = %+v", info)
	}
	if info.Status != sextantproto.WorktreeStatusActive {
		t.Errorf("Status = %s", info.Status)
	}
	// On-disk worktree exists.
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatalf("worktree path: %v", err)
	}
	// Registry has one entry.
	raw, ok := reg.entries["feat-hello-world-001"]
	if !ok {
		t.Fatal("registry entry missing")
	}
	var got sextantproto.WorktreeInfo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// List returns the one entry.
	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "feat-hello-world-001" {
		t.Errorf("List = %+v", list)
	}

	// Write a file in the worktree + commit + diff.
	if err := os.WriteFile(filepath.Join(info.Path, "hello.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runOrFail(t, info.Path, "git", "add", "hello.txt")
	runOrFail(t, info.Path, "git", "config", "user.email", "test@example.com")
	runOrFail(t, info.Path, "git", "config", "user.name", "tester")
	runOrFail(t, info.Path, "git", "commit", "-m", "add hello")

	diff, err := m.Diff(ctx, "feat-hello-world-001", "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "hello.txt") || !strings.Contains(diff, "+hi") {
		t.Errorf("diff missing expected content: %q", diff)
	}

	// Destroy without force on an active worktree → rejected.
	if err := m.Destroy(ctx, "feat-hello-world-001", false); !errors.Is(err, worktree.ErrStatusGuard) {
		t.Errorf("Destroy without force: %v", err)
	}

	// Destroy with force → succeeds.
	if err := m.Destroy(ctx, "feat-hello-world-001", true); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, ok := reg.entries["feat-hello-world-001"]; ok {
		t.Error("registry entry not removed")
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree dir still exists: %v", err)
	}
	_ = repo
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	m, _, _, _ := buildManager(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, "feat-dup-name-001", "main", uuid.Nil); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := m.Create(ctx, "feat-dup-name-001", "main", uuid.Nil)
	if !errors.Is(err, worktree.ErrAlreadyExists) {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	m, _, _, _ := buildManager(t)
	_, err := m.Create(context.Background(), "not-a-valid-name", "main", uuid.Nil)
	if !errors.Is(err, worktree.ErrInvalidName) {
		t.Errorf("expected ErrInvalidName, got %v", err)
	}
}

func TestMergeCleanPath(t *testing.T) {
	m, reg, _, repo := buildManager(t)
	ctx := context.Background()

	info, err := m.Create(ctx, "feat-clean-merge-001", "main", uuid.Nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Commit one new file on the branch.
	if err := os.WriteFile(filepath.Join(info.Path, "added.txt"), []byte("added\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runOrFail(t, info.Path, "git", "config", "user.email", "test@example.com")
	runOrFail(t, info.Path, "git", "config", "user.name", "tester")
	runOrFail(t, info.Path, "git", "add", "added.txt")
	runOrFail(t, info.Path, "git", "commit", "-m", "add file on branch")

	res, err := m.Merge(ctx, "feat-clean-merge-001", "main")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !res.OK {
		t.Errorf("MergeResult.OK = false; conflicts=%v", res.Conflicts)
	}
	if len(res.Conflicts) != 0 {
		t.Errorf("Conflicts = %v", res.Conflicts)
	}

	// main now contains added.txt.
	mainBlob := runCapture(t, repo, "git", "show", "main:added.txt")
	if mainBlob != "added" {
		t.Errorf("main:added.txt = %q", mainBlob)
	}

	// KV status flipped to merged.
	raw := reg.entries["feat-clean-merge-001"]
	var post sextantproto.WorktreeInfo
	_ = json.Unmarshal(raw, &post)
	if post.Status != sextantproto.WorktreeStatusMerged {
		t.Errorf("Status post-merge = %s", post.Status)
	}

	// No stale .merge-* dirs left behind.
	worktreesRoot := filepath.Dir(info.Path)
	entries, _ := os.ReadDir(worktreesRoot)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".merge-") {
			t.Errorf("stale merge worktree left behind: %s", e.Name())
		}
	}
}

func TestMergeConflictPath(t *testing.T) {
	m, reg, _, repo := buildManager(t)
	ctx := context.Background()

	// Set up: branch creates conflict.txt with "branch", main mutates
	// the same file to "main" after branch was forked.
	info, err := m.Create(ctx, "feat-conflict-merge-001", "main", uuid.Nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "conflict.txt"), []byte("branch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, info.Path, "git", "config", "user.email", "test@example.com")
	runOrFail(t, info.Path, "git", "config", "user.name", "tester")
	runOrFail(t, info.Path, "git", "add", "conflict.txt")
	runOrFail(t, info.Path, "git", "commit", "-m", "branch version")

	// Mutate main directly so the two branches both add the same
	// file with different content.
	if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOrFail(t, repo, "git", "add", "conflict.txt")
	runOrFail(t, repo, "git", "commit", "-m", "main version")

	res, err := m.Merge(ctx, "feat-conflict-merge-001", "main")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if res.OK {
		t.Error("expected conflict; got OK=true")
	}
	if len(res.Conflicts) == 0 {
		t.Error("Conflicts is empty")
	}

	// KV status flipped to conflict.
	raw := reg.entries["feat-conflict-merge-001"]
	var post sextantproto.WorktreeInfo
	_ = json.Unmarshal(raw, &post)
	if post.Status != sextantproto.WorktreeStatusConflict {
		t.Errorf("Status post-conflict = %s", post.Status)
	}

	// No stale .merge-* dirs left behind even on conflict.
	worktreesRoot := filepath.Dir(info.Path)
	entries, _ := os.ReadDir(worktreesRoot)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".merge-") {
			t.Errorf("stale merge worktree left behind: %s", e.Name())
		}
	}
}

func TestSpawnWorktreeName(t *testing.T) {
	u := uuid.MustParse("abcdef01-2345-6789-abcd-ef0123456789")
	got := worktree.SpawnWorktreeName("default", u)
	want := "feat-default-abcdef01-001"
	if got != want {
		t.Errorf("SpawnWorktreeName = %q, want %q", got, want)
	}
	// Spawn-style names must validate so the round-trip via Create works.
	if err := worktree.ValidateName(got); err != nil {
		t.Errorf("ValidateName(%q): %v", got, err)
	}
	// Template name with hyphens still produces a valid result.
	got2 := worktree.SpawnWorktreeName("multi-step", u)
	if err := worktree.ValidateName(got2); err != nil {
		t.Errorf("ValidateName(%q): %v", got2, err)
	}
}

func TestAcquireMergeLockBasic(t *testing.T) {
	locks := newFakeKV()
	release, err := worktree.AcquireMergeLock(context.Background(), locks, "holder-a", time.Minute, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// Second acquire while first is held returns ErrLockHeld.
	if _, err := worktree.AcquireMergeLock(context.Background(), locks, "holder-b", time.Minute, nil); !errors.Is(err, worktree.ErrLockHeld) {
		t.Errorf("second Acquire: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	// After release, another acquire works.
	rel2, err := worktree.AcquireMergeLock(context.Background(), locks, "holder-c", time.Minute, nil)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	_ = rel2()
}

// TestMergeIsSerializedByLock proves the load-bearing claim from
// `specs/architecture.md` §11: worktree_merge serializes through
// `locks.merge`. Two concurrent Merge() calls on distinct
// worktrees against the same target must not overlap their
// lock-holding windows. We observe the lock directly via the
// shared `locks` KV: while either Merge goroutine is mid-flight,
// an external AcquireMergeLock against the same bucket must
// return ErrLockHeld. If both merges proceeded without taking
// the lock, every external acquire would succeed.
//
// Both merges land cleanly at the end; the test doesn't pin an
// order — what matters is that mutual exclusion is honored.
func TestMergeIsSerializedByLock(t *testing.T) {
	m, _, locks, _ := buildManager(t)
	ctx := context.Background()

	infoA, err := m.Create(ctx, "feat-serial-a-001", "main", uuid.Nil)
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	infoB, err := m.Create(ctx, "feat-serial-b-001", "main", uuid.Nil)
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	// Commit one distinct file on each worktree so the merges
	// produce observable side-effects.
	for _, w := range []struct {
		path, file, content string
	}{
		{infoA.Path, "a.txt", "alpha\n"},
		{infoB.Path, "b.txt", "beta\n"},
	} {
		if err := os.WriteFile(filepath.Join(w.path, w.file), []byte(w.content), 0o600); err != nil {
			t.Fatalf("write %s: %v", w.file, err)
		}
		runOrFail(t, w.path, "git", "config", "user.email", "test@example.com")
		runOrFail(t, w.path, "git", "config", "user.name", "tester")
		runOrFail(t, w.path, "git", "add", w.file)
		runOrFail(t, w.path, "git", "commit", "-m", "add "+w.file)
	}

	type result struct {
		who string
		res worktree.MergeResult
		err error
	}
	resCh := make(chan result, 2)
	go func() {
		r, err := m.Merge(ctx, "feat-serial-a-001", "main")
		resCh <- result{"a", r, err}
	}()
	go func() {
		r, err := m.Merge(ctx, "feat-serial-b-001", "main")
		resCh <- result{"b", r, err}
	}()

	// While either Merge is in flight it must hold the lock. Poll
	// the `locks` bucket directly: a non-empty `merge` key proves
	// somebody has the lock. We require at least one external
	// observation of the lock-held state — if neither Merge ever
	// held the lock the bucket would be empty throughout.
	//
	// Run the polling loop in its own goroutine so we don't race
	// the merge completions. The merges hold the lock for hundreds
	// of milliseconds (real git work) so a 5ms poll gives us tens
	// of observation chances per merge.
	pollDone := make(chan struct{})
	stopPoll := make(chan struct{})
	var sawHeld atomic.Bool
	var externalLockErrs atomic.Int32
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-stopPoll:
				return
			default:
			}
			if _, err := locks.Get(ctx, worktree.MergeLockKey); err == nil {
				sawHeld.Store(true)
				// Assert an external AcquireMergeLock against the
				// same bucket fails with ErrLockHeld — strongest
				// evidence the lock is functional, not a bystander
				// record. Count failures so the test can assert
				// at least one happened.
				_, lockErr := worktree.AcquireMergeLock(ctx, locks, "external-prober", time.Second, time.Now)
				if errors.Is(lockErr, worktree.ErrLockHeld) {
					externalLockErrs.Add(1)
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Both merges eventually complete. Either order is fine; both
	// must report OK=true.
	got := map[string]result{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-resCh:
			got[r.who] = r
		case <-time.After(30 * time.Second):
			close(stopPoll)
			<-pollDone
			t.Fatalf("merge goroutine did not finish (have %d/2: %v)", len(got), got)
		}
	}
	close(stopPoll)
	<-pollDone

	if !sawHeld.Load() {
		t.Error("locks.merge was never populated; Merge did not hold the lock")
	}
	if externalLockErrs.Load() == 0 {
		t.Error("external AcquireMergeLock never returned ErrLockHeld while a Merge was in flight; lock is not functional")
	}
	for _, who := range []string{"a", "b"} {
		r := got[who]
		if r.err != nil {
			t.Errorf("merge %s error: %v", who, r.err)
		}
		if !r.res.OK {
			t.Errorf("merge %s OK=false (conflicts=%v)", who, r.res.Conflicts)
		}
	}

	// Post-conditions: the lock is released and both merges left no
	// stale merge worktrees behind.
	if _, err := locks.Get(ctx, worktree.MergeLockKey); err == nil {
		t.Error("locks.merge still set after both merges completed")
	}
	entries, _ := os.ReadDir(filepath.Dir(infoA.Path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".merge-") {
			t.Errorf("stale merge worktree leftover: %s", e.Name())
		}
	}
}

func TestAcquireMergeLockTTLExpiry(t *testing.T) {
	locks := newFakeKV()
	// Inject `now` so the first acquire writes an old timestamp. The
	// stale-lock cleanup in AcquireMergeLock should then reclaim it.
	pastNow := func() time.Time { return time.Now().UTC().Add(-time.Hour) }
	rel, err := worktree.AcquireMergeLock(context.Background(), locks, "stale-holder", time.Second, pastNow)
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	// Even though we never released, a fresh acquirer with a present
	// `now` sees the TTL has elapsed and reclaims.
	rel2, err := worktree.AcquireMergeLock(context.Background(), locks, "fresh-holder", time.Second, time.Now)
	if err != nil {
		t.Fatalf("reclaim stale lock: %v", err)
	}
	_ = rel()
	_ = rel2()
}
