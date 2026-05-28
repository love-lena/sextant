package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// ContainerLister is the narrow surface the reconciler needs on
// *containermgr.Manager. Tests substitute a fake.
type ContainerLister interface {
	List(ctx context.Context, f containermgr.Filter) ([]containermgr.ContainerInfo, error)
}

// ReconcilerDefsKV is the read+list KV surface the reconciler needs.
// It does not need writes — the LifecycleWatcher handles those when it
// receives the synthetic envelopes Reconciler publishes.
type ReconcilerDefsKV interface {
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	ListKeys(ctx context.Context, opts ...jetstream.WatchOpt) (jetstream.KeyLister, error)
}

// ReconcileResult counts what Reconciler.Run did.
type ReconcileResult struct {
	// Scanned is the total number of KV keys iterated.
	Scanned int
	// RunningRecords is the number of definitions whose lifecycle was
	// "running" (the only state the reconciler acts on).
	RunningRecords int
	// MissingContainers is the number of running definitions whose
	// (uuid, incarnation_id) pair had no live container on this host.
	MissingContainers int
	// Published is the number of synthetic transition=lost envelopes
	// successfully passed to the Publish function.
	Published int
}

// Reconciler walks agent_definitions at daemon startup, queries
// containermgr for the set of running sidecar containers on this host,
// and publishes synthetic transition=lost envelopes for agent records
// whose (uuid, incarnation_id) pair is absent from the container set.
// The LifecycleWatcher receives those envelopes and writes the updated
// state back to KV with its CAS/yield guarantees.
//
// Reconciler is single-use: call Run once during daemon startup before
// starting the LifecycleWatcher subscription.
type Reconciler struct {
	// Defs is the agent_definitions KV bucket (read + list only).
	Defs ReconcilerDefsKV
	// Mgr is the container runtime used to list live sidecars on this host.
	Mgr ContainerLister
	// Publish delivers a synthetic lifecycle envelope to the bus. The
	// closure is responsible for deriving the NATS subject from the
	// envelope's payload — typically `agents.<payload.AgentUUID>.lifecycle`
	// — because Envelope itself does not carry a Subject field. The
	// LifecycleWatcher reads the subject-routed envelope and writes the
	// state to KV under its CAS + yield guards.
	Publish func(ctx context.Context, env sextantproto.Envelope) error
	// HostID is the daemon's host identifier, used to filter containers
	// to only those belonging to this host via LabelHostID.
	HostID string
}

// Run performs a single reconciliation pass. It:
//  1. Lists all sidecar containers on this host from containermgr.
//  2. Builds a present-set keyed by "agent_uuid|incarnation_id".
//  3. Iterates every key in agent_definitions.
//  4. For each running definition whose (uuid, incarnation_id) is not in
//     the present-set, publishes a synthetic transition=lost envelope.
//
// Returns a ReconcileResult with counters and nil on success (even if
// some individual envelopes failed to publish — those are logged).
// Returns a non-nil error only when the KV bucket or container list
// cannot be read at all.
func (r *Reconciler) Run(ctx context.Context) (ReconcileResult, error) {
	if r.Defs == nil || r.Mgr == nil || r.Publish == nil {
		return ReconcileResult{}, fmt.Errorf("reconciler: missing dependency")
	}

	// Step 1: query the container runtime for all sidecar containers
	// stamped with LabelHostID matching this daemon's host. The filter
	// scopes results to this host so multi-host deployments don't
	// incorrectly mark agents running on a peer as lost.
	containers, err := r.Mgr.List(ctx, containermgr.Filter{
		Labels: map[string]string{
			handlers.LabelHostID: r.HostID,
		},
	})
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("reconciler: list containers: %w", err)
	}

	// Step 2: build a present-set keyed by "agent_uuid|incarnation_id".
	// Both labels must be set; containers missing either are skipped
	// (they were not spawned by sextant or predate label stamping).
	present := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		uuidStr := c.Labels[handlers.LabelAgentUUID]
		incStr := c.Labels[handlers.LabelIncarnationID]
		if uuidStr == "" || incStr == "" {
			continue
		}
		present[uuidStr+"|"+incStr] = struct{}{}
	}

	// Step 3: iterate every key in agent_definitions.
	lister, err := r.Defs.ListKeys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrNoKeysFound) {
			// Empty bucket is fine — nothing to reconcile.
			return ReconcileResult{}, nil
		}
		return ReconcileResult{}, fmt.Errorf("reconciler: list keys: %w", err)
	}
	defer func() { _ = lister.Stop() }()

	var res ReconcileResult
	for key := range lister.Keys() {
		res.Scanned++

		entry, err := r.Defs.Get(ctx, key)
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				// Key disappeared between ListKeys and Get; skip.
				continue
			}
			log.Printf("sextantd: reconciler: get %s: %v", key, err)
			continue
		}

		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(entry.Value(), &def); err != nil {
			log.Printf("sextantd: reconciler: decode %s: %v", key, err)
			continue
		}

		// Only running definitions need reconciliation. Terminal and
		// paused states are intentional and must not be disturbed.
		if def.Lifecycle != sextantproto.LifecycleRunning {
			continue
		}
		res.RunningRecords++

		// CurrentIncarnationID zero means the record predates the
		// incarnation-ID field. The watcher's warm-up path handles
		// these; the reconciler skips them to avoid spurious lost
		// envelopes.
		if def.CurrentIncarnationID == uuid.Nil {
			continue
		}

		// Step 4: check whether the (uuid, incarnation_id) pair has a
		// live container on this host.
		needle := def.UUID.String() + "|" + def.CurrentIncarnationID.String()
		if _, ok := present[needle]; ok {
			continue
		}
		res.MissingContainers++

		env, err := buildLostEnvelope(
			def.UUID, def.CurrentIncarnationID,
			sextantproto.LifecycleSourceReconciler,
			"container absent at daemon startup",
		)
		if err != nil {
			log.Printf("sextantd: reconciler: build envelope for %s: %v", key, err)
			continue
		}
		if err := r.Publish(ctx, env); err != nil {
			log.Printf("sextantd: reconciler: publish for %s: %v", key, err)
			continue
		}
		res.Published++
	}
	return res, nil
}

// buildLostEnvelope packages a synthetic transition=lost lifecycle
// envelope for the given agent and incarnation. Shared between
// Reconciler and the future ContainerWatcher (L3).
//
// The From address uses AddressDaemon so consumers can distinguish
// daemon-generated envelopes from sidecar-generated ones.
func buildLostEnvelope(
	agentID, incarnationID uuid.UUID,
	source sextantproto.LifecycleSource,
	reason string,
) (sextantproto.Envelope, error) {
	from := sextantproto.Address{
		Kind: sextantproto.AddressDaemon,
		ID:   agentID.String(),
	}
	payload := sextantproto.LifecyclePayload{
		AgentUUID:     agentID,
		IncarnationID: incarnationID,
		Transition:    sextantproto.LifecycleLostEvent,
		State:         sextantproto.IncarnationFailed,
		Source:        source,
		Reason:        reason,
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindLifecycle, from, payload)
	if err != nil {
		return sextantproto.Envelope{}, fmt.Errorf("build lost envelope: %w", err)
	}
	return env, nil
}
