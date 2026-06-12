package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The tap-qualified formula name. Upgrading by the qualified name works whether
// or not the tap is the default, and is unambiguous if a same-named formula
// ever appears in another tap.
const brewFormula = "love-lena/sextant/sextant"

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
		fmt.Fprint(stderr, notBrewMsg)
		return nil
	}
	if !brewInstalled() {
		fmt.Fprint(stderr, notBrewMsg)
		return nil
	}

	fmt.Fprintf(stdout, "updating Homebrew and upgrading %s…\n\n", brewFormula)
	if err := runBrew(stdout, stderr, brewBin, "update"); err != nil {
		return fmt.Errorf("brew update: %w", err)
	}
	if err := runBrew(stdout, stderr, brewBin, "upgrade", brewFormula); err != nil {
		return fmt.Errorf("brew upgrade %s: %w", brewFormula, err)
	}
	return nil
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
  • from source:       go install github.com/love-lena/sextant/cmd/... (in a clone: git pull && go install ./cmd/...)
  • release tarball:    re-download the latest from
                        https://github.com/love-lena/sextant/releases

To switch to the Homebrew-managed install:
  brew tap love-lena/sextant https://github.com/love-lena/sextant
  brew install sextant
`
