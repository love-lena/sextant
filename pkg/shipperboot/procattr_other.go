//go:build !linux

package shipperboot

import "syscall"

// shipperProcAttr puts the shipper in its own process group so Stop can
// signal the whole tree. Pdeathsig (parent-death signal) is a Linux-only
// facility; on other platforms (e.g. Darwin, the local dev/test host) the
// kernel offers no equivalent, so a HARD-killed daemon can still leak the
// shipper. The daemon's graceful-shutdown path (cmd.Cancel + Stop) and the
// supervisor's stopShipperNow safety net remain the reap mechanism there.
func shipperProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
