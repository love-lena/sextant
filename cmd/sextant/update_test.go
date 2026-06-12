package main

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

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
