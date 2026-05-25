package containermgr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
)

// requireDocker skips the test if no Docker socket is reachable. The
// containermgr tests exercise a real daemon: we want to surface real
// integration breakage, not paper over it with a fake. CI runs without
// Docker; the skip keeps `make test` green there. Local runs (OrbStack
// up) exercise the real path.
func requireDocker(t *testing.T) {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(SocketEnvVar))
	if path == "" {
		path = detectSocketPath()
	}
	if path == "" {
		t.Skip("no Docker socket detected (OrbStack / Docker Desktop not running)")
	}
	if st, err := os.Stat(path); err != nil || st.Mode()&os.ModeSocket == 0 {
		t.Skipf("docker socket %s missing or not a socket", path)
	}
}

// alpineImage is the image the test runs. Pinned to a digest-stable
// tag so a remote registry hiccup doesn't surface as a test flake. We
// intentionally use a tiny image so the test does not stall on a big
// layer pull when the cache is cold.
const alpineImage = "alpine:3.20"

// pullIfMissing pulls alpineImage when it's not already present so the
// test works on a cold cache. Best-effort; failures here surface as the
// Run call's image-not-found, which we'd rather see explicitly.
func pullIfMissing(t *testing.T, mgr *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	imgs, err := mgr.cli.ImageList(ctx, image.ListOptions{})
	if err == nil {
		for _, img := range imgs {
			for _, tag := range img.RepoTags {
				if tag == alpineImage {
					return
				}
			}
		}
	}
	rc, err := mgr.cli.ImagePull(ctx, alpineImage, image.PullOptions{})
	if err != nil {
		t.Skipf("pull %s: %v (skipping)", alpineImage, err)
	}
	defer rc.Close() //nolint:errcheck // best-effort close
	// Drain so the pull actually completes.
	_, _ = io.Copy(io.Discard, rc)
}

func TestRunsAndStopsContainer(t *testing.T) {
	requireDocker(t)

	mgr, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	pullIfMissing(t, mgr)

	runID := "ctest-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + filepath.Base(t.TempDir())
	spec := ContainerSpec{
		Name:       runID,
		Image:      alpineImage,
		Cmd:        []string{"sh", "-c", "echo ready && sleep 30"},
		Env:        map[string]string{"FOO": "bar"},
		Labels:     map[string]string{"sextant.test_run": runID},
		AutoRemove: false,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := mgr.Run(ctx, spec)
	// t.Cleanup registers force-remove BEFORE any assertion so a
	// mid-test failure doesn't leak the container.
	t.Cleanup(func() {
		_ = mgr.ForceRemoveByLabel(context.Background(), "sextant.test_run", runID)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.ID == "" {
		t.Fatal("Run returned empty container ID")
	}

	info, err := mgr.Inspect(ctx, c.ID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Status != "running" {
		t.Errorf("Status = %q, want running", info.Status)
	}
	if info.Labels["sextant.test_run"] != runID {
		t.Errorf("missing test_run label: %v", info.Labels)
	}

	list, err := mgr.List(ctx, Filter{Labels: map[string]string{"sextant.test_run": runID}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(List) = %d, want 1", len(list))
	}
	if list[0].ID != c.ID {
		t.Fatalf("List ID = %s, want %s", list[0].ID, c.ID)
	}

	if err := mgr.Stop(ctx, c.ID, 5*time.Second); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop the container should be gone — Inspect must error.
	if _, err := mgr.Inspect(ctx, c.ID); err == nil {
		t.Errorf("Inspect after Stop unexpectedly succeeded")
	}

	// And List filtered by the label should be empty.
	list, err = mgr.List(ctx, Filter{Labels: map[string]string{"sextant.test_run": runID}})
	if err != nil {
		t.Fatalf("List post-stop: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("post-stop list = %d entries, want 0", len(list))
	}
}

func TestStopMissingContainerSucceeds(t *testing.T) {
	requireDocker(t)
	mgr, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	// Stop on an obviously-missing ID should not error — the post-state
	// (no such container) is the post-state we wanted.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.Stop(ctx, "definitely-not-a-real-container-id-zzz", 5*time.Second); err != nil {
		t.Fatalf("Stop missing container: %v", err)
	}
}

func TestNewFailsWithBadSocketPath(t *testing.T) {
	// Force New to look at a non-existent socket; the Ping should fail.
	dir := t.TempDir()
	_, err := New(Config{SocketPath: filepath.Join(dir, "missing.sock")})
	if err == nil {
		t.Fatal("expected an error from New with a bad socket path")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("err = %v, want wrap of ErrDaemonUnavailable", err)
	}
}
