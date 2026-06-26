package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Ops is the primitive bus surface a convention verb is written against — the
// subset of the SDK's operations a verb may issue, named exactly as the SDK
// names them. A verb written against Ops runs unchanged against two
// implementations: the SDK adapter (SDKOps in sdkops.go — a real *Client, so
// the verb works live) and the Recorder (which captures instead of issuing, so
// the verb is recorded into a vector).
//
// This indirection is what lets a verb be conformance-recorded without the
// recorder importing the SDK, and without the verb knowing whether it is live
// or being recorded — the engine-as-a-library posture of ADR-0041. Records and
// payloads are json.RawMessage here; wire.Lexicon is an alias for it, so the
// SDK adapter is a straight pass-through.
//
// Every method corresponds to a primitive operation in protocol/methods.json.
// A verb that needs an operation not yet on this interface extends it (and the
// adapter and recorder alongside) — the interface is the verb-author's
// contract, deliberately small.
type Ops interface {
	// Publish issues a message.publish on subject (must be msg.*) with record.
	Publish(ctx context.Context, subject string, record json.RawMessage) error
	// CreateArtifact issues an artifact.create; returns the new revision.
	CreateArtifact(ctx context.Context, name string, record json.RawMessage) (uint64, error)
	// UpdateArtifact issues a compare-and-set artifact.update; returns the new revision.
	UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error)
	// GetArtifact issues an artifact.get; returns the record and its revision.
	GetArtifact(ctx context.Context, name string) (record json.RawMessage, revision uint64, err error)
	// DeleteArtifact issues an artifact.delete.
	DeleteArtifact(ctx context.Context, name string) error
	// ListArtifacts issues an artifact.list; returns artifact names.
	ListArtifacts(ctx context.Context) ([]string, error)
}

// Verb runs a convention verb against ops, decoding its domain arguments from
// the vector's raw `input`. A convention library exposes its verbs and a tiny
// adapter wraps each as a Verb for the conformance suite. The verb issues
// primitive operations through ops; against a Recorder those become the
// transcript a vector pins.
//
// A Verb returns an error only for a genuine verb failure (bad input, a
// precondition a real verb would reject); the operations it issued up to that
// point are still captured. The runner treats a verb error as a vector failure.
type Verb func(ctx context.Context, ops Ops, input json.RawMessage) error

// Registry maps (convention, verb) to a Go Verb. A convention's test package
// registers its verbs once, then hands the registry to ReplayVectors, which
// looks up each vector's (Convention, Verb) to find the function to replay. The
// registry is the seam TASK-173 plugs the real goals verbs into: conv/goals
// registers setCriterion, setStatus, … here, and the existing vectors replay
// against them with no runner change.
type Registry struct {
	mu    sync.RWMutex
	verbs map[string]Verb
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{verbs: map[string]Verb{}}
}

// Register binds a verb under (convention, verb). It panics on a duplicate
// registration — two verbs answering the same vector is a wiring bug, not a
// runtime condition.
func (r *Registry) Register(convention, verb string, fn Verb) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := convention + "/" + verb
	if _, dup := r.verbs[key]; dup {
		panic(fmt.Sprintf("conformance: verb %q registered twice", key))
	}
	r.verbs[key] = fn
}

// Lookup returns the verb bound to (convention, verb), or ok=false.
func (r *Registry) Lookup(convention, verb string) (Verb, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.verbs[convention+"/"+verb]
	return fn, ok
}

// Conventions returns the distinct convention names the registry knows, sorted.
func (r *Registry) Conventions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	for key := range r.verbs {
		conv := key
		if i := indexByte(key, '/'); i >= 0 {
			conv = key[:i]
		}
		seen[conv] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
