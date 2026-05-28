package handlers

import (
	"context"
	"os"
	"time"

	pkgversion "github.com/love-lena/sextant/pkg/version"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// VersionDeps is the dep bag for the get_version handler.
//
// StartedAt captures the daemon process start time — the handler closes
// over it so a long-running daemon reports the moment it actually came
// up, not the moment of the call. PID is read from os.Getpid() at call
// time so a future host-restart story (where the daemon process id
// changes mid-flight under the same start time) keeps reporting the
// live pid.
//
// DaemonVersion / Commit / ProtoVersion default to the package globals
// when the dep bag leaves them empty. Tests override them to assert
// rendering without relying on -ldflags being honored by `go test`.
type VersionDeps struct {
	StartedAt     time.Time
	DaemonVersion string
	Commit        string
	ProtoVersion  string
	PIDFn         func() int
}

// NewGetVersion returns a Handler for the get_version verb. It always
// succeeds — the diagnostic value of the call is "the daemon answered",
// so the handler never errors. Payload is ignored; the verb is parameter-
// less today.
func NewGetVersion(deps VersionDeps) rpc.Handler {
	if deps.DaemonVersion == "" {
		deps.DaemonVersion = pkgversion.Version
	}
	if deps.Commit == "" {
		deps.Commit = pkgversion.Commit
	}
	if deps.ProtoVersion == "" {
		deps.ProtoVersion = sextantproto.ProtoVersion
	}
	if deps.PIDFn == nil {
		deps.PIDFn = os.Getpid
	}
	return func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		return emitOK(emit, sextantproto.GetVersionResponse{
			DaemonVersion: deps.DaemonVersion,
			Commit:        deps.Commit,
			ProtoVersion:  deps.ProtoVersion,
			StartedAt:     deps.StartedAt.UTC(),
			PID:           int64(deps.PIDFn()),
		})
	}
}
