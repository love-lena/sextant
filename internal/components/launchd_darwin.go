//go:build darwin

package components

import (
	"fmt"
	"os"
	"os/exec"
)

// launchd_darwin.go is the macOS service plane: the real launchctl Runner and
// the uid/home resolution. The OS-independent orchestration lives in service.go
// (over the injected Runner), so this file is thin.

// Supported reports that managed components run on this OS.
func Supported() bool { return true }

// launchctl is the production Runner: it shells out to launchctl and returns the
// combined output so callers can parse `print` and surface errors verbatim.
func launchctl(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// NewManager builds a Manager bound to the current user's gui domain. self is
// the running sextant binary's path (the plist re-execs into it via
// `components exec`). It fails loud if the home dir is unresolvable rather than
// writing a plist to the wrong place.
func NewManager(self string) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, fmt.Errorf("cannot resolve home dir for ~/Library/LaunchAgents: %w", err)
	}
	return &Manager{UID: os.Getuid(), Home: home, Self: self, Run: launchctl}, nil
}
