//go:build !darwin

package components

import (
	"fmt"
	"runtime"
)

// launchd_other.go is the non-macOS stub: launchctl is the only service plane
// this slice implements (the live setup is macOS), so managed components are
// unsupported elsewhere — a clear error, not a systemd path (out of scope for
// v0.5.3).

// Supported reports that managed components do not run on this OS.
func Supported() bool { return false }

// NewManager refuses on non-macOS with a clear message.
func NewManager(self string) (*Manager, error) {
	return nil, fmt.Errorf("sextant components: managed services are macOS-only in v0.5.3 "+
		"(no launchd on %s); run the runtimes manually, e.g. `sextant-workflow --creds F --store DIR`", runtime.GOOS)
}
