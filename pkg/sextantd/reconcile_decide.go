package sextantd

import (
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// actionKind enumerates the single next step the pure reconcile core
// decides on. The reconciler executes exactly one per pass — level
// reconciliation converges by repeated single steps, never a batch.
type actionKind int

const (
	// actionNone: observed already matches desired; nothing to do. The
	// idempotence oracle (RFC §5.9) is "reconcile twice ⇒ second is
	// actionNone."
	actionNone actionKind = iota
	// actionActuate: desired=run but there is no healthy live incarnation
	// for the current spec/nonce — build + run a fresh container (via the
	// C0 single-source builder). Covers the initial spawn, a restart
	// nonce bump, and a spec-generation re-actuation. P0 does NOT actuate
	// out of a terminal observation (lost/crashed/ended) — that is the
	// P1 recovery branch.
	actionActuate
	// actionStop: desired=paused and a live container exists — stop it.
	// The record is retained; the name stays held.
	actionStop
	// actionTeardown: desired=archived — stop any live container, remove
	// it, release the name + clean per-agent volumes. Terminal.
	actionTeardown
	// actionMarkLost: desired=run, the record believes a container should
	// be live (observed running/pending) but none is present and no
	// sidecar terminal was observed — write observed=lost. P0 stops here
	// (no auto-restart); P1 turns this into the recovery branch.
	actionMarkLost
)

func (a actionKind) String() string {
	switch a {
	case actionNone:
		return "none"
	case actionActuate:
		return "actuate"
	case actionStop:
		return "stop"
	case actionTeardown:
		return "teardown"
	case actionMarkLost:
		return "mark_lost"
	default:
		return "unknown"
	}
}

// actualState is the reconciler's re-observation of container reality
// for one agent, gathered fresh every pass (level-triggered, RFC §3.2).
// It is deliberately small: the decision core depends only on facts the
// fake docker in unit tests can supply.
type actualState struct {
	// ContainerPresent reports whether a live (running or created)
	// container exists for the agent's current incarnation.
	ContainerPresent bool
	// ContainerRunning reports whether that container is in docker's
	// "running" status (vs created/exited). Only meaningful when
	// ContainerPresent.
	ContainerRunning bool
	// SidecarTerminalObserved reports whether the sidecar published a
	// terminal lifecycle (ended/crashed) for the current incarnation —
	// the "sidecar-observed terminal OUTRANKS daemon-inferred lost"
	// invariant carried forward from the lifecycle watcher. When true the
	// reconciler must NOT downgrade the observed terminal to `lost`.
	SidecarTerminalObserved bool
	// SidecarTerminalState is the specific terminal the sidecar reported
	// (ended/crashed), valid only when SidecarTerminalObserved. The
	// reconciler converges observed to this value.
	SidecarTerminalState sextantproto.ObservedState
}

// decision is the pure verdict: the action plus the observed-state the
// reconciler should converge the record toward. Writing observed is the
// reconciler's job alone (single-writer, RFC §5.2).
type decision struct {
	Action actionKind
	// Observed is the status the reconciler should record. Zero value
	// ("") means "leave observed unchanged" (e.g. actionStop while the
	// container is still draining).
	Observed sextantproto.ObservedState
}

// decideAction is the PURE reconcile core (RFC §5.1, §5.9). Given the
// desired spec, the current observed status, and the freshly re-observed
// container reality, it computes the single next action and the observed
// state to converge toward. No I/O, no clock, no container calls — every
// branch is a unit test (inject desired + fake docker, assert the
// action). The reconciler (reconcile.go) is the thin imperative shell
// that gathers `actual`, calls this, and applies the verdict.
//
// The whole job, in Appendix C's terms: drive status toward spec.
//   - desired=archived            → tear down (terminal intent wins)
//   - desired=paused, running     → stop
//   - desired=run, no live healthy container, fresh spec → actuate
//   - desired=run, was-running but container vanished → mark lost
//   - converged                   → none
func decideAction(def sextantproto.AgentDefinition, actual actualState) decision {
	spec := def.Spec
	status := def.Status

	switch spec.Desired {
	case sextantproto.DesiredArchived:
		// Archive is terminal intent. Tear down while any container or a
		// non-archived observation remains; once observed==ended and no
		// container is present we are converged.
		if actual.ContainerPresent || status.Observed != sextantproto.ObservedEnded {
			return decision{Action: actionTeardown, Observed: sextantproto.ObservedEnded}
		}
		return decision{Action: actionNone}

	case sextantproto.DesiredPaused:
		// Paused intent: ensure no live container, retain the record.
		if actual.ContainerPresent {
			return decision{Action: actionStop, Observed: sextantproto.ObservedEnded}
		}
		// No container. If we still believe it is running/pending,
		// converge the observation to ended (the operator paused it).
		if status.Observed == sextantproto.ObservedRunning || status.Observed == sextantproto.ObservedPending {
			return decision{Action: actionNone, Observed: sextantproto.ObservedEnded}
		}
		return decision{Action: actionNone}

	case sextantproto.DesiredRun, "":
		// "" treated as run for forward-compat / legacy warm-up.
		return decideRun(spec, status, actual)

	default:
		// Unknown desired value — do nothing rather than guess.
		return decision{Action: actionNone}
	}
}

// decideRun is the desired=run branch (the hot path). Split out so the
// nonce / generation / liveness reasoning stays readable.
//
// "Never actuated" is keyed on Status.CurrentIncarnationID == Nil: until
// the reconciler has built an incarnation, there is no live thing to
// have lost, so the no-container case is an initial actuation, not a
// loss. A sidecar-observed terminal always wins the precedence contest.
func decideRun(spec sextantproto.AgentSpec, status sextantproto.AgentStatusRecord, actual actualState) decision {
	neverActuated := status.CurrentIncarnationID == uuid.Nil

	// (1) A fresh actuation is owed when the reconciler has not yet caught
	// up to the latest spec generation or reactuation nonce. This is the
	// restart path: `restart` bumps spec.reactuation_nonce, and
	// observed_nonce < reactuation_nonce means a fresh incarnation must be
	// built. A spec edit bumps generation; observed_generation <
	// generation means the same. Either gap forces a re-actuation
	// REGARDLESS of whether a container is currently live (a running agent
	// on a stale spec must be replaced). Spawn writes generation=1 /
	// observed_generation=0, so this is also the initial-spawn trigger.
	if status.ObservedNonce < spec.ReactuationNonce ||
		status.ObservedGeneration < spec.Generation {
		return decision{Action: actionActuate, Observed: sextantproto.ObservedPending}
	}

	// (2) Caught up to spec. Now it is a liveness question.
	if actual.ContainerRunning {
		// Healthy: a live running container exists. Converge observed to
		// running (idempotent when already running).
		return decision{Action: actionNone, Observed: sextantproto.ObservedRunning}
	}

	if actual.ContainerPresent {
		// Container exists but is not running (created / restarting /
		// exited-but-not-yet-removed). Treat as still pending — give it a
		// tick to come up rather than tearing it down. The periodic sweep
		// re-checks; once it is gone we fall through to the loss branch.
		return decision{Action: actionNone, Observed: sextantproto.ObservedPending}
	}

	// (3) No container present.
	if actual.SidecarTerminalObserved {
		// The sidecar published ended/crashed. That observed terminal
		// OUTRANKS a daemon-inferred lost — converge observed to the
		// reported terminal (never downgrade to lost). P0 does not
		// auto-restart a terminal agent.
		return decision{Action: actionNone, Observed: actual.SidecarTerminalState}
	}

	if neverActuated {
		// Caught up to spec (generation==observed_generation, e.g. both 0)
		// yet no incarnation has ever been built — actuate the first one.
		return decision{Action: actionActuate, Observed: sextantproto.ObservedPending}
	}

	switch status.Observed {
	case sextantproto.ObservedEnded, sextantproto.ObservedCrashed:
		// Already a sidecar-observed terminal — same precedence rule as
		// above even when the hint flag has aged out of the actual probe.
		// No re-actuation in P0 (recovery is P1).
		return decision{Action: actionNone}
	case sextantproto.ObservedLost:
		// Already lost and converged. P0 leaves a lost agent lost
		// (auto-recovery is restored by feat-ctl-p1-recovery).
		return decision{Action: actionNone}
	default:
		// We had a live incarnation (running/pending) and the container is
		// gone with no observed cause. Infer lost.
		return decision{Action: actionMarkLost, Observed: sextantproto.ObservedLost}
	}
}
