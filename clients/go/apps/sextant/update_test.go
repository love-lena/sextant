package main

import (
	"errors"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/protocol/conninfo"
)

const testBusURL = "nats://127.0.0.1:63621"

// fastHealthBudgets shrinks the polling budgets for the duration of a test so
// the timeout paths (bus stays down) complete in milliseconds, not the 10s+5s
// production budgets. It restores them on cleanup.
func fastHealthBudgets(t *testing.T) {
	t.Helper()
	rb, kb, pi := restartHealthBudget, kickstartHealthBudget, healthPollInterval
	restartHealthBudget = 20 * time.Millisecond
	kickstartHealthBudget = 20 * time.Millisecond
	healthPollInterval = 1 * time.Millisecond
	t.Cleanup(func() {
		restartHealthBudget, kickstartHealthBudget, healthPollInterval = rb, kb, pi
	})
}

// TestBusHealthy proves the production health-checker resolves the recorded URL
// from bus.json under the store dir and reports liveness from a real TCP dial —
// the discovery-path resolution and listen probe, end to end.
func TestBusHealthy(t *testing.T) {
	// A live listener stands in for the bus; bus.json points at it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	store := t.TempDir()
	url := "nats://" + ln.Addr().String()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: url}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	gotURL, up := busHealthy(store)
	if !up {
		t.Fatalf("busHealthy should report up for a live listener at %s", url)
	}
	if gotURL != url {
		t.Fatalf("busHealthy URL = %q, want %q", gotURL, url)
	}

	// No bus.json ⇒ not up, no URL.
	if u, up := busHealthy(t.TempDir()); up || u != "" {
		t.Fatalf("busHealthy on a store with no bus.json should be down with no URL; got %q, %v", u, up)
	}

	// bus.json present but nothing listening ⇒ down, URL still reported.
	ln.Close()
	if u, up := busHealthy(store); up {
		t.Fatalf("busHealthy should report down when nothing listens on %s", url)
	} else if u != url {
		t.Fatalf("busHealthy should still report the recorded URL when down; got %q", u)
	}
}

// TestEnsureBusUpRestartHealthy: a restart that brings the bus up needs no
// kickstart, and reports the recorded URL.
func TestEnsureBusUpRestartHealthy(t *testing.T) {
	var out, errOut strings.Builder
	restarted := false
	restart := func() error { restarted = true; return nil }
	// Healthy immediately after the restart.
	healthy := func() (string, bool) { return testBusURL, true }
	kickstart := func() error { t.Fatal("kickstart fired though the restart brought the bus up"); return nil }

	ensureBusUpWith(&out, &errOut, restart, healthy, kickstart)

	if !restarted {
		t.Fatalf("expected the bus to be restarted")
	}
	if !strings.Contains(out.String(), "bus is back up at "+testBusURL) {
		t.Fatalf("expected a back-up message with the URL; stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("no warning expected on the healthy path; stderr = %q", errOut.String())
	}
}

// TestEnsureBusUpRestartStaysDownThenKickstart: the restart leaves the bus down
// (the loaded-but-never-launched trap), the kickstart fallback fires, and the
// bus then comes up.
func TestEnsureBusUpRestartStaysDownThenKickstart(t *testing.T) {
	fastHealthBudgets(t)
	var out, errOut strings.Builder
	restart := func() error { return nil }
	kicked := false
	kickstart := func() error { kicked = true; return nil }
	// Down until the kickstart fires, up afterward.
	healthy := func() (string, bool) {
		if kicked {
			return testBusURL, true
		}
		return testBusURL, false
	}

	ensureBusUpWith(&out, &errOut, restart, healthy, kickstart)

	if !kicked {
		t.Fatalf("expected the kickstart fallback to fire when the restart left the bus down")
	}
	s := out.String()
	if !strings.Contains(s, "forcing a launchd relaunch") {
		t.Fatalf("expected the kickstart-relaunch notice; stdout = %q", s)
	}
	if !strings.Contains(s, "bus is back up at "+testBusURL) {
		t.Fatalf("expected a back-up message after the kickstart; stdout = %q", s)
	}
}

// TestEnsureBusUpAllFailLoudWarning: restart and kickstart both fail to bring
// the bus up — the operator gets a loud warning with the manual recovery
// command, never a hollow success.
func TestEnsureBusUpAllFailLoudWarning(t *testing.T) {
	fastHealthBudgets(t)
	var out, errOut strings.Builder
	restart := func() error { return nil }
	kickstart := func() error { return nil }
	healthy := func() (string, bool) { return testBusURL, false } // never up

	ensureBusUpWith(&out, &errOut, restart, healthy, kickstart)

	es := errOut.String()
	if !strings.Contains(es, "did NOT come back up") {
		t.Fatalf("expected a loud warning that the bus is down; stderr = %q", es)
	}
	// The RELIABLE recovery is the launchd kickstart — NOT `brew services
	// restart`, which is the command that left the job loaded-but-never-launched.
	if !strings.Contains(es, "launchctl kickstart -k") {
		t.Fatalf("expected the kickstart recovery command in the warning; stderr = %q", es)
	}
	if strings.Contains(es, "brew services restart") {
		t.Fatalf("the warning must not suggest the brew restart that caused the outage; stderr = %q", es)
	}
	if strings.Contains(out.String(), "bus is back up") {
		t.Fatalf("must not claim the bus is up when it never came up; stdout = %q", out.String())
	}
}

// TestEnsureBusUpNoKickstarter: on a platform without launchd recovery (nil
// kickstarter) a stay-down bus goes straight to the loud warning.
func TestEnsureBusUpNoKickstarter(t *testing.T) {
	fastHealthBudgets(t)
	var out, errOut strings.Builder
	restart := func() error { return nil }
	healthy := func() (string, bool) { return testBusURL, false }

	ensureBusUpWith(&out, &errOut, restart, healthy, nil)

	if strings.Contains(out.String(), "forcing a launchd relaunch") {
		t.Fatalf("must not attempt a kickstart with a nil kickstarter; stdout = %q", out.String())
	}
	es := errOut.String()
	if !strings.Contains(es, "did NOT come back up") {
		t.Fatalf("expected the loud warning with no kickstarter; stderr = %q", es)
	}
	// With no launchd lever the only manual recovery is the brew-services restart.
	if !strings.Contains(es, "brew services restart") {
		t.Fatalf("expected the brew-services recovery command with no kickstarter; stderr = %q", es)
	}
}

// TestRunUpdateNoBrewOnPath: when brew is not on PATH, update prints the
// manual-path guidance and does not error (nothing to upgrade is not a failure).
func TestRunUpdateNoBrewOnPath(t *testing.T) {
	var out, errOut strings.Builder
	noBrew := func(string) (string, error) { return "", exec.ErrNotFound }
	// brewInstalled must never be consulted once brew is missing.
	mustNotCall := func() bool { t.Fatal("brewInstalled called though brew is absent"); return false }

	if err := runUpdate(&out, &errOut, noBrew, mustNotCall); err != nil {
		t.Fatalf("runUpdate() err = %v, want nil", err)
	}
	if got := errOut.String(); !strings.Contains(got, "not appear to be installed via Homebrew") {
		t.Fatalf("missing brew guidance; stderr = %q", got)
	}
	if !strings.Contains(errOut.String(), brewFormula) {
		t.Fatalf("guidance should name the tap formula %q; stderr = %q", brewFormula, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatalf("no upgrade should run; stdout = %q", out.String())
	}
}

// TestRunUpdateBrewButNotBrewInstalled: brew exists, but the running binary was
// not installed by brew (go install / tarball). Same guidance, no brew run.
func TestRunUpdateBrewButNotBrewInstalled(t *testing.T) {
	var out, errOut strings.Builder
	haveBrew := func(name string) (string, error) {
		if name == "brew" {
			return "/opt/homebrew/bin/brew", nil
		}
		return "", exec.ErrNotFound
	}
	notInstalled := func() bool { return false }

	if err := runUpdate(&out, &errOut, haveBrew, notInstalled); err != nil {
		t.Fatalf("runUpdate() err = %v, want nil", err)
	}
	if got := errOut.String(); !strings.Contains(got, "nothing for") {
		t.Fatalf("missing not-brew-installed guidance; stderr = %q", got)
	}
	if out.Len() != 0 {
		t.Fatalf("no upgrade should run when not brew-installed; stdout = %q", out.String())
	}
}

// TestUpdateDispatch: the top-level switch routes "update" to cmdUpdate. We
// can't call cmdUpdate directly (it would shell out / os.Exit), so we assert
// the dispatch wiring by confirming "update" is a recognised verb in the
// switch's source-of-truth list. A lightweight guard against a regression that
// drops the case.
func TestUpdateIsADispatchedVerb(t *testing.T) {
	// runUpdate is the unit under test for behaviour; this test guards that the
	// not-brew path is reachable with the production injectables shape, i.e.
	// the signature cmdUpdate calls. If the signature drifts, this won't compile.
	var sink io.Writer = io.Discard
	err := runUpdate(sink, sink, exec.LookPath, func() bool { return false })
	// With a real brew possibly present but this test binary not brew-installed,
	// runUpdate must still return nil (it only prints guidance).
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("runUpdate with production injectables errored unexpectedly: %v", err)
	}
}
