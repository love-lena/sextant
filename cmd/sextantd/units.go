package main

import (
	"context"
	"errors"

	"github.com/love-lena/sextant-initial/pkg/clickhouseboot"
	"github.com/love-lena/sextant-initial/pkg/natsboot"
	"github.com/love-lena/sextant-initial/pkg/shipperboot"
)

// natsProcess adapts a natsboot.Server to the supervisor.Process
// interface. Wait blocks on the server's Done channel; Stop forwards
// to natsboot's graceful-shutdown path.
type natsProcess struct {
	srv *natsboot.Server
}

func newNATSProcess(srv *natsboot.Server) *natsProcess {
	return &natsProcess{srv: srv}
}

func (p *natsProcess) Wait() error {
	<-p.srv.Done()
	if err := p.srv.ExitErr(); err != nil {
		return err
	}
	// A clean Wait exit still means "subprocess died" from the
	// supervisor's perspective; the supervisor checks its own Stop
	// flag to distinguish graceful shutdown from unexpected exit.
	return errSubprocessExited
}

func (p *natsProcess) Stop(ctx context.Context) error {
	return p.srv.Stop(ctx)
}

// clickhouseProcess adapts clickhouseboot.Server analogously.
type clickhouseProcess struct {
	srv *clickhouseboot.Server
}

func newClickHouseProcess(srv *clickhouseboot.Server) *clickhouseProcess {
	return &clickhouseProcess{srv: srv}
}

func (p *clickhouseProcess) Wait() error {
	<-p.srv.Done()
	if err := p.srv.ExitErr(); err != nil {
		return err
	}
	return errSubprocessExited
}

func (p *clickhouseProcess) Stop(ctx context.Context) error {
	return p.srv.Stop(ctx)
}

// shipperProcess adapts a shipperboot.Server to supervisor.Process.
// Same pattern as natsProcess / clickhouseProcess.
type shipperProcess struct {
	srv *shipperboot.Server
}

func newShipperProcess(srv *shipperboot.Server) *shipperProcess {
	return &shipperProcess{srv: srv}
}

func (p *shipperProcess) Wait() error {
	<-p.srv.Done()
	if err := p.srv.ExitErr(); err != nil {
		return err
	}
	return errSubprocessExited
}

func (p *shipperProcess) Stop(ctx context.Context) error {
	return p.srv.Stop(ctx)
}

// errSubprocessExited is returned by supervised-unit Wait paths when
// the subprocess died with a nil exit error (e.g. it was SIGKILLed from
// outside the daemon and natsboot/clickhouseboot folded the exit into
// nil). The supervisor must still see this as "exited" to advance its
// retry counter, so we synthesize a sentinel.
var errSubprocessExited = errors.New("subprocess exited")
