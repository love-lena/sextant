package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/love-lena/sextant/clients/sextant-cli/internal/components"
	"github.com/love-lena/sextant/protocol/conninfo"
)

// The tap-qualified formula name. Upgrading by the qualified name works whether
// or not the tap is the default, and is unambiguous if a same-named formula
// ever appears in another tap.
const brewFormula = "love-lena/sextant/sextant"

// brewService is the unqualified service name `brew services` operates on. The
// formula is tap-qualified for upgrade, but `brew services <verb>` takes the
// bare formula name.
const brewService = "sextant"

// The polling budgets for ensureBusUp's two recovery stages. After the restart
// the bus normally comes up within a second or two; the kickstart fallback gets
// a shorter window since it is the last resort before warning. They are vars,
// not consts, so a test can shrink them and exercise the timeout paths fast.
var (
	restartHealthBudget   = 10 * time.Second
	kickstartHealthBudget = 5 * time.Second
	healthPollInterval    = 250 * time.Millisecond
)

// cmdUpdate upgrades a Homebrew-installed sextant to the latest tap version.
// It is a thin convenience over `brew upgrade` so an operator does not have to
// remember the tap-qualified formula name; non-brew installs (go install, raw
// tarball) get a clear pointer to their own upgrade path rather than a
// confusing brew error.
func cmdUpdate(args []string) {
	if len(args) > 0 {
		fatal("update takes no arguments")
	}
	if err := runUpdate(os.Stdout, os.Stderr, exec.LookPath, brewInstalledSelf); err != nil {
		fatal("%v", err)
	}
}

// runUpdate is the testable core: it decides whether a brew upgrade applies and
// runs `brew update` then `brew upgrade <formula>`. lookPath and brewInstalled
// are injected so tests can exercise the decision without a real brew on PATH.
func runUpdate(stdout, stderr io.Writer, lookPath func(string) (string, error), brewInstalled func() bool) error {
	brewBin, err := lookPath("brew")
	if err != nil {
		_, _ = fmt.Fprint(stderr, notBrewMsg)
		return nil
	}
	if !brewInstalled() {
		_, _ = fmt.Fprint(stderr, notBrewMsg)
		return nil
	}

	_, _ = fmt.Fprintf(stdout, "updating Homebrew and upgrading %s…\n\n", brewFormula)
	if err := runBrew(stdout, stderr, brewBin, "update"); err != nil {
		return fmt.Errorf("brew update: %w", err)
	}
	if err := runBrew(stdout, stderr, brewBin, "upgrade", brewFormula); err != nil {
		return fmt.Errorf("brew upgrade %s: %w", brewFormula, err)
	}

	// `brew upgrade` stops the bus service to swap the binary, then re-bootstraps
	// the launchd job — but the job can land loaded-but-never-launched (runs = 0,
	// "last exit = (never exited)"), leaving every client stranded behind a dead
	// bus. The upgrade is not done until the bus is actually back up, so bring it
	// back and verify it rather than reporting a hollow success.
	ensureBusUp(stdout, stderr, brewBin, defaultStore())
	return nil
}

// ensureBusUp re-launches and health-checks the bus after a binary swap. It
// wires the production restart / health / kickstart actions and defers to
// ensureBusUpWith for the testable orchestration.
func ensureBusUp(stdout, stderr io.Writer, brewBin, store string) {
	restart := func() error { return runBrew(stdout, stderr, brewBin, "services", "restart", brewService) }
	healthy := func() (string, bool) { return busHealthy(store) }
	var kickstart func() error
	if runtime.GOOS == "darwin" {
		kickstart = runKickstart
	}
	ensureBusUpWith(stdout, stderr, restart, healthy, kickstart)
}

// ensureBusUpWith is the testable core. It restarts the bus, polls for it to
// come up, and falls back to a launchd kickstart if the restart alone did not
// relaunch the job. restart, healthy, and kickstart are injected so a test can
// drive all three paths (restart-then-healthy, restart-stays-down → kickstart →
// healthy, all-fail → loud warning) without a real brew/launchd/bus.
//
// healthy returns the recorded bus URL and whether the bus is up. kickstart is
// nil on platforms with no launchd recovery; the all-fail warning then names the
// manual recovery instead.
func ensureBusUpWith(stdout, stderr io.Writer, restart func() error, healthy func() (string, bool), kickstart func() error) {
	_, _ = fmt.Fprintf(stdout, "\nbringing the bus back up after the upgrade…\n")
	if err := restart(); err != nil {
		// A failed restart is not fatal on its own — the kickstart fallback may
		// still recover it — so note it and press on to the health check.
		_, _ = fmt.Fprintf(stderr, "  warning: `brew services restart %s` failed: %v\n", brewService, err)
	}

	if url, up := pollHealthy(healthy, restartHealthBudget); up {
		_, _ = fmt.Fprintf(stdout, "  bus is back up at %s\n", url)
		return
	}

	// The restart left the job loaded-but-not-launched (the post-upgrade trap).
	// On macOS a launchd kickstart forces the relaunch; elsewhere there is no
	// such recovery and we go straight to the loud warning.
	if kickstart != nil {
		_, _ = fmt.Fprintf(stdout, "  bus did not come up after restart; forcing a launchd relaunch…\n")
		if err := kickstart(); err != nil {
			_, _ = fmt.Fprintf(stderr, "  warning: launchd kickstart failed: %v\n", err)
		} else if url, up := pollHealthy(healthy, kickstartHealthBudget); up {
			_, _ = fmt.Fprintf(stdout, "  bus is back up at %s\n", url)
			return
		}
	}

	warnBusDown(stderr, kickstart != nil)
}

// pollHealthy polls healthy until it reports up or the budget elapses. It
// returns the recorded bus URL and true on success, or "", false if the bus
// never came up in time. The bounded-poll loop is shared with the components
// runtime health check (components.PollUntil): both apply the loaded-but-runs=0
// lesson (#211) — never trust a restart/kickstart to have actually launched;
// poll for the real signal, then warn loud if it never comes.
func pollHealthy(healthy func() (string, bool), budget time.Duration) (string, bool) {
	var url string
	up := components.PollUntil(func() bool {
		u, ok := healthy()
		if ok {
			url = u
		}
		return ok
	}, budget, healthPollInterval)
	if !up {
		return "", false
	}
	return url, true
}

// busHealthy reports the recorded bus URL and whether something is listening on
// it. It resolves the discovery file (bus.json) under the store dir, parses its
// URL, and TCP-dials the host:port — the same liveness signal `sextant doctor`
// uses. A missing/unreadable discovery file or an empty address counts as
// not-up (and returns no URL).
func busHealthy(store string) (string, bool) {
	url := busURL(store)
	addr := hostPort(url)
	if addr == "" {
		return "", false
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return url, false
	}
	_ = conn.Close()
	return url, true
}

// busURL reads the recorded bus URL from the discovery file (bus.json) under the
// store dir, or "" if it is missing/unreadable. The path is resolved the same
// way every client resolves it — never hardcoded.
func busURL(store string) string {
	info, err := conninfo.Read(filepath.Join(store, conninfo.DefaultFile))
	if err != nil {
		return ""
	}
	return info.URL
}

// kickstartTarget is the bus's per-user launchd gui-domain target. It reuses
// `sextant doctor`'s launchdLabel so update fixes exactly what doctor diagnoses.
func kickstartTarget() string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel) }

// runKickstart forces a launchd relaunch of the bus job. It is macOS-only (the
// caller passes a nil kickstarter elsewhere); `launchctl kickstart -k` stops the
// job if running and starts it fresh, which is what clears the loaded-but-never-
// launched state the upgrade leaves behind.
func runKickstart() error {
	return exec.Command("launchctl", "kickstart", "-k", kickstartTarget()).Run()
}

// warnBusDown prints a loud, explicit warning that the bus did not come back,
// with the exact RELIABLE manual recovery command. Never let the operator
// believe the upgrade succeeded when the bus is still dead.
//
// When a launchd kickstart was available (kickstartAvailable — the macOS path
// where the auto-kickstart was attempted), the manual lever is that same
// kickstart, NOT `brew services restart`: the restart is exactly what left the
// job loaded-but-never-launched, and the kickstart is what actually relaunches
// it. Where no kickstart lever exists (non-macOS), the brew-services restart is
// the only option, so name that there.
func warnBusDown(stderr io.Writer, kickstartAvailable bool) {
	_, _ = fmt.Fprintf(stderr, "\n  WARNING: the upgrade completed but the bus did NOT come back up.\n")
	_, _ = fmt.Fprintf(stderr, "  Every client is stranded until it is relaunched. Recover it with:\n\n")
	if kickstartAvailable {
		_, _ = fmt.Fprintf(stderr, "    launchctl kickstart -k %s\n\n", kickstartTarget())
	} else {
		_, _ = fmt.Fprintf(stderr, "    brew services restart %s\n\n", brewService)
	}
	_, _ = fmt.Fprintf(stderr, "  Then check it with: sextant doctor\n")
}

// runBrew streams a brew invocation's output through to the caller's streams so
// the operator sees brew's own progress.
func runBrew(stdout, stderr io.Writer, brewBin string, args ...string) error {
	c := exec.Command(brewBin, args...)
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// brewInstalledSelf reports whether the running sextant binary lives under a
// Homebrew prefix (…/Cellar/… or a brew opt/bin symlink). Only then does a
// `brew upgrade` make sense; a go-install or raw-tarball binary is upgraded its
// own way.
func brewInstalledSelf() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	// Resolve symlinks: brew puts the binary in the Cellar and links it onto
	// PATH via opt/bin, so the real path is the reliable signal.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return strings.Contains(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator))
}

const notBrewMsg = `sextant does not appear to be installed via Homebrew, so there is nothing for
'sextant update' to upgrade.

  • Homebrew install:  brew upgrade ` + brewFormula + `
  • from source:       go install github.com/love-lena/sextant/clients/... (in a clone: git pull && go install ./clients/...)
  • release tarball:    re-download the latest from
                        https://github.com/love-lena/sextant/releases

To switch to the Homebrew-managed install:
  brew tap love-lena/sextant https://github.com/love-lena/sextant
  brew install sextant
`
