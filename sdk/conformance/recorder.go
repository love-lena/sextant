// Package conformance is the Go side of the conformance suite (ADR-0041): the
// recording client a convention verb runs against and the runner that replays
// the language-neutral vectors under protocol/conformance/vectors.
//
// It lives in client land, not in protocol/, on purpose. The vector FORMAT and
// the vector FILES are language-neutral and ship with the protocol
// (protocol/conformance). But the runner *invokes a convention verb* to replay
// a transcript, and a verb is a client-side library over the SDK. Putting the
// runner in protocol/ would make the protocol import a client and break
// importcheck's "the bus and protocol never depend on a client" edge (ADR-0041).
// So the split is: data + rule in protocol/conformance, behaviour (recorder +
// replay) here. The Go SDK is not imported here either — the recorder
// implements the same primitive surface a verb calls, decoupled by the Ops
// interface a verb is written against (see verb.go).
//
// This package is convention-AGNOSTIC: a convention's test package registers
// its verbs (or passes them to ReplayVectors) and this package replays whatever
// vectors it is pointed at. The goals convention and its real vectors land in
// TASK-173; until then a fixture verb in this package's own tests proves the
// machinery end to end.
package conformance

import (
	"context"
	"encoding/json"

	pconf "github.com/love-lena/sextant/protocol/conformance"
)

// Recorder captures the primitive bus operations a verb performs instead of
// issuing them to a real bus. It implements Ops (see verb.go) — the same
// primitive surface the SDK's *Client offers — so a verb written against Ops
// runs unchanged against a Recorder. Each call appends one entry to Ops, in
// call order; that ordered slice IS the transcript a vector records and the
// runner compares against.
//
// A Recorder is single-use and not safe for concurrent verbs; a verb is a
// straight-line sequence of operations, recorded on one goroutine.
type Recorder struct {
	ops []pconf.Op
	// seeded is the recorder's stub artifact store: enough state for a
	// read-then-write verb to see a plausible prior value and revision without a
	// real bus. A create starts an artifact at revision 1; an update advances it
	// to expectedRev+1; a get returns the seeded value. State a verb DEPENDS on
	// before its first write is loaded with SeedArtifact (recording setup, not
	// transcript). The recorder is a stub, not a faithful CAS store — a verb's
	// correctness must not hinge on revision arithmetic the bus would do.
	seeded map[string]seededArtifact
}

type seededArtifact struct {
	record   json.RawMessage
	revision uint64
}

// NewRecorder returns an empty Recorder.
func NewRecorder() *Recorder {
	return &Recorder{seeded: map[string]seededArtifact{}}
}

// SeedArtifact pre-loads an artifact so a verb that reads-then-writes (the
// common goal pattern: get the goal, mutate a criterion, update) sees a
// realistic prior value during recording. It does not appear in the transcript;
// it is recording setup, mirroring the bus state a verb would find live. The
// vector's `input` plus any seeded state together determine the transcript.
func (r *Recorder) SeedArtifact(name string, record json.RawMessage, revision uint64) {
	r.seeded[name] = seededArtifact{record: record, revision: revision}
}

// Operations returns the captured transcript, in call order.
func (r *Recorder) Operations() []pconf.Op { return r.ops }

// --- Ops implementation: the recorded primitive surface ---

// Publish records a message.publish. Subject and payload are captured; the
// record is stored as the operation's canonical payload.
func (r *Recorder) Publish(_ context.Context, subject string, record json.RawMessage) error {
	r.ops = append(r.ops, pconf.Op{
		Op:      "message.publish",
		Subject: subject,
		Payload: cloneRaw(record),
	})
	return nil
}

// CreateArtifact records an artifact.create and returns revision 1, the stub
// first revision a freshly created artifact reports.
func (r *Recorder) CreateArtifact(_ context.Context, name string, record json.RawMessage) (uint64, error) {
	r.ops = append(r.ops, pconf.Op{
		Op:      "artifact.create",
		Name:    name,
		Payload: cloneRaw(record),
	})
	r.seeded[name] = seededArtifact{record: cloneRaw(record), revision: 1}
	return 1, nil
}

// UpdateArtifact records an artifact.update (carrying the compare-and-set
// revision) and returns the advanced stub revision.
func (r *Recorder) UpdateArtifact(_ context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	rev := expectedRev
	r.ops = append(r.ops, pconf.Op{
		Op:          "artifact.update",
		Name:        name,
		Payload:     cloneRaw(record),
		ExpectedRev: &rev,
	})
	next := expectedRev + 1
	r.seeded[name] = seededArtifact{record: cloneRaw(record), revision: next}
	return next, nil
}

// GetArtifact records an artifact.get and returns seeded state if present. A get
// is itself an observable operation (a verb that reads before writing emits it),
// so it is captured. With no seeded state it returns a zero-value artifact and
// revision 0 — enough for a verb to proceed; seed state for read-dependent
// verbs.
func (r *Recorder) GetArtifact(_ context.Context, name string) (json.RawMessage, uint64, error) {
	r.ops = append(r.ops, pconf.Op{Op: "artifact.get", Name: name})
	if s, ok := r.seeded[name]; ok {
		return cloneRaw(s.record), s.revision, nil
	}
	return nil, 0, nil
}

// DeleteArtifact records an artifact.delete.
func (r *Recorder) DeleteArtifact(_ context.Context, name string) error {
	r.ops = append(r.ops, pconf.Op{Op: "artifact.delete", Name: name})
	delete(r.seeded, name)
	return nil
}

// ListArtifacts records an artifact.list. It returns seeded names (sorted-ish by
// insertion is not promised; a verb that lists is rare and should not depend on
// order from the recorder). The list itself is the observable operation.
func (r *Recorder) ListArtifacts(_ context.Context) ([]string, error) {
	r.ops = append(r.ops, pconf.Op{Op: "artifact.list"})
	names := make([]string, 0, len(r.seeded))
	for n := range r.seeded {
		names = append(names, n)
	}
	return names, nil
}

// cloneRaw copies a json.RawMessage so the recorder's captured payload can't be
// mutated by a verb reusing its buffer.
func cloneRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}

// Compile-time assertion that Recorder satisfies the Ops surface a verb is
// written against.
var _ Ops = (*Recorder)(nil)
