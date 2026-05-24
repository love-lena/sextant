package rpc

import "github.com/love-lena/sextant-initial/pkg/sextantproto"

// CapabilityChecker decides whether a request envelope carries the
// capability required for the verb. M10 implements the real JWT-backed
// check; M7 ships AllowAll as the default so operator-path RPCs over
// the Unix-perm-trusted NATS connection (architecture.md §10b) work
// without a JWT.
//
// The Server invokes Check pre-dispatch. A non-nil error becomes an
// RPCError{Code: "capability_denied"} reply and the handler never runs.
type CapabilityChecker interface {
	Check(req sextantproto.Envelope, requiredCap string) error
}

// AllowAll is the M7 default checker — it returns nil for every request.
// Operator-path requests over the Unix-perm-trusted NATS connection
// inherit operator authority via the trust boundary on the creds file
// (architecture.md §10b), so cap checking is a no-op here.
//
// M10 swaps this for a real JWT-backed checker that verifies the
// caller's signed capability set against requiredCap before dispatch.
type AllowAll struct{}

// Check always returns nil — see type doc.
func (AllowAll) Check(_ sextantproto.Envelope, _ string) error { return nil }
