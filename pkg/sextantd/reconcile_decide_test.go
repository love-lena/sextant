package sextantd

import (
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// liveIncarnation is a non-nil incarnation id for "has been actuated"
// fixtures. The exact value never matters to the pure decision core.
var liveIncarnation = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// def builds an AgentDefinition fixture from a spec + status. Keeping a
// helper keeps each convergence case a one-liner so the table reads as a
// truth table over the single record (RFC §5.9: convergence is a unit
// test).
func def(spec sextantproto.AgentSpec, status sextantproto.AgentStatusRecord) sextantproto.AgentDefinition {
	return sextantproto.AgentDefinition{
		UUID:   uuid.New(),
		Name:   "t",
		Spec:   spec,
		Status: status,
	}
}

func TestDecideAction_Convergence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		def        sextantproto.AgentDefinition
		actual     actualState
		wantAction actionKind
		wantObs    sextantproto.ObservedState // "" = don't assert
	}{
		{
			name: "initial spawn: desired=run, never actuated, no container -> actuate",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedPending},
			),
			actual:     actualState{},
			wantAction: actionActuate,
			wantObs:    sextantproto.ObservedPending,
		},
		{
			name: "spawn with gen 0 and nil incarnation -> actuate (never-actuated trigger)",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun},
				sextantproto.AgentStatusRecord{},
			),
			actual:     actualState{},
			wantAction: actionActuate,
		},
		{
			name: "healthy: desired=run, caught up, container running -> none/running",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{ContainerPresent: true, ContainerRunning: true},
			wantAction: actionNone,
			wantObs:    sextantproto.ObservedRunning,
		},
		{
			name: "out-of-band kill: desired=run, was running, container gone, no sidecar terminal -> mark lost",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionMarkLost,
			wantObs:    sextantproto.ObservedLost,
		},
		{
			name: "sidecar terminal outranks lost: container gone but sidecar published terminal -> none (no downgrade)",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedEnded,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{SidecarTerminalObserved: true, SidecarTerminalState: sextantproto.ObservedEnded},
			wantAction: actionNone,
			wantObs:    sextantproto.ObservedEnded,
		},
		{
			name: "sidecar terminal precedence persists once observed=crashed even without the live hint",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedCrashed,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionNone,
		},
		{
			name: "lost stays lost: P0 does NOT auto-restart (recovery is P1)",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedLost,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionNone,
		},
		{
			name: "restart nonce bump: observed_nonce < reactuation_nonce -> actuate fresh incarnation",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1, ReactuationNonce: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
					ObservedNonce:        0,
				},
			),
			// Container still running on the OLD incarnation; the nonce gap
			// must force a re-actuation anyway.
			actual:     actualState{ContainerPresent: true, ContainerRunning: true},
			wantAction: actionActuate,
			wantObs:    sextantproto.ObservedPending,
		},
		{
			name: "spec generation bump forces re-actuation even while running",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 2},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{ContainerPresent: true, ContainerRunning: true},
			wantAction: actionActuate,
		},
		{
			name: "stop (paused): desired=paused, container live -> stop",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredPaused, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{ContainerPresent: true, ContainerRunning: true},
			wantAction: actionStop,
		},
		{
			name: "stop converged: desired=paused, no container -> none",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredPaused, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedEnded,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionNone,
		},
		{
			name: "archive: desired=archived, container live -> teardown",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{ContainerPresent: true, ContainerRunning: true},
			wantAction: actionTeardown,
			wantObs:    sextantproto.ObservedEnded,
		},
		{
			name: "archive converged: desired=archived, observed=ended, no container -> none",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedEnded,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionNone,
		},
		{
			name: "archive still tears down a non-ended record even with no container",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedRunning,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{},
			wantAction: actionTeardown,
		},
		{
			name: "container present but not running -> none/pending (give it a tick)",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{
					Observed:             sextantproto.ObservedPending,
					CurrentIncarnationID: liveIncarnation,
					ObservedGeneration:   1,
				},
			),
			actual:     actualState{ContainerPresent: true, ContainerRunning: false},
			wantAction: actionNone,
			wantObs:    sextantproto.ObservedPending,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideAction(tc.def, tc.actual)
			if got.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", got.Action, tc.wantAction)
			}
			if tc.wantObs != "" && got.Observed != tc.wantObs {
				t.Fatalf("observed = %q, want %q", got.Observed, tc.wantObs)
			}
		})
	}
}

// TestDecideAction_Idempotent is the idempotence oracle (RFC §5.9): once
// the record reflects the action's converged observed state, the next
// decision must be actionNone. Running reconcile twice is a no-op.
func TestDecideAction_Idempotent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		def    sextantproto.AgentDefinition
		actual actualState
	}{
		{
			name: "running healthy is stable",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
			),
			actual: actualState{ContainerPresent: true, ContainerRunning: true},
		},
		{
			name: "lost is stable (no flap back to actuate)",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
				sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedLost, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
			),
			actual: actualState{},
		},
		{
			name: "archived+ended is stable",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1},
				sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
			),
			actual: actualState{},
		},
		{
			name: "paused+ended is stable",
			def: def(
				sextantproto.AgentSpec{Desired: sextantproto.DesiredPaused, Generation: 1},
				sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded, CurrentIncarnationID: liveIncarnation, ObservedGeneration: 1},
			),
			actual: actualState{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideAction(tc.def, tc.actual)
			if got.Action != actionNone {
				t.Fatalf("steady-state decision = %v, want none (not idempotent)", got.Action)
			}
		})
	}
}

// TestDecideAction_RestartNonceAppliedIsConverged proves the nonce
// semantics close the loop: after the reconciler stamps observed_nonce
// to match reactuation_nonce, the restart is converged.
func TestDecideAction_RestartNonceAppliedIsConverged(t *testing.T) {
	t.Parallel()

	d := def(
		sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1, ReactuationNonce: 3},
		sextantproto.AgentStatusRecord{
			Observed:             sextantproto.ObservedRunning,
			CurrentIncarnationID: liveIncarnation,
			ObservedGeneration:   1,
			ObservedNonce:        3, // reconciler has applied the nonce
		},
	)
	got := decideAction(d, actualState{ContainerPresent: true, ContainerRunning: true})
	if got.Action != actionNone {
		t.Fatalf("after applying nonce, decision = %v, want none", got.Action)
	}
}
