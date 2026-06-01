//go:build linux

package shipperboot

import "syscall"

// shipperProcAttr puts the shipper in its own process group (so Stop can
// signal the whole tree) AND sets Pdeathsig=SIGKILL: when the parent
// daemon dies — including a HARD kill (SIGKILL) where no Go cleanup runs —
// the kernel delivers SIGKILL to the shipper, so it cannot survive as a
// PPID=1 orphan. This is the production (Linux) reaping guarantee.
func shipperProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
}
