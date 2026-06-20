package goals_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	conf "github.com/love-lena/sextant/clients/go/conformance"
	"github.com/love-lena/sextant/clients/go/conventions/goals"
	pconf "github.com/love-lena/sextant/protocol/conformance"
)

// The goals convention's conformance suite. It plugs the REAL goal verbs into the
// TASK-183 seam: register them in a conformance.Registry, then ReplayVectors over
// the language-neutral vectors under protocol/conformance/vectors/goals — no
// runner change. These replace the fixture vector as the first real vectors; a
// TypeScript conv/goals (TASK-175) replays the SAME files to prove it is co-equal.

// vectorsDir is the protocol-owned vector root, relative to this test
// (clients/go/conventions/goals → repo root → protocol/conformance/vectors).
func vectorsDir() string {
	return filepath.Join("..", "..", "..", "..", "protocol", "conformance", "vectors")
}

// fixedNow is the timestamp the recorded setCriterion verb stamps, so the
// captured goal.update is byte-stable across runs (time.Now would churn every
// -update). The live SetCriterion takes the real time; only the recorded verb
// pins it.
const fixedNow = "2026-06-19T00:00:00Z"

// setCriterionVerb adapts goals.SetCriterion to a conformance.Verb: decode the
// vector's input, run the verb against the recorder (conf.Ops's method set is a
// superset of goals.Ops, so the recorder is the verb's Ops unchanged), and stamp
// the fixed timestamp so the transcript is deterministic.
func setCriterionVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var in goals.SetCriterionInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("goals conformance: decode setCriterion input: %w", err)
	}
	_, err := goals.SetCriterion(ctx, ops, in, fixedNow)
	return err
}

func goalsRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register("goals", "setCriterion", setCriterionVerb)
	return reg
}

// seedGoal seeds the recorder with the prior goal artifact a setCriterion vector
// reads before it writes. SetCriterion is a read-then-write verb (get the goal,
// mutate a criterion, update); the seed mirrors the bus state it would find live
// and does not appear in the transcript. The seeded goal has the criterion the
// vector flips in a not-yet-target status (so the verb does real work) at the
// revision the vector's artifact.update CAS's against.
func seedGoal(rec *conf.Recorder, v pconf.OpTranscriptVector) {
	if v.Verb != "setCriterion" {
		return
	}
	var in goals.SetCriterionInput
	_ = json.Unmarshal(v.Input, &in)
	// A goal with the target criterion plus a sibling, so the transcript shows the
	// rewrite preserves siblings and the north-star. The criterion starts
	// not-started, so setting any status is a real transition.
	goal := goals.Goal{
		Northstar: "Ship the goals convention",
		Criteria: []goals.Criterion{
			{ID: in.CriterionID, Text: "the criterion under test", Status: goals.StatusNotStarted, Owner: "sirius"},
			{ID: "other", Text: "a sibling criterion", Status: goals.StatusInProgress},
		},
	}
	record, _ := json.Marshal(goal)
	// Revision 4 is the value the vector's artifact.update expectedRev pins.
	rec.SeedArtifact(goals.ArtifactName(in.GoalID), record, 4)
}

// TestGoalsConformance replays the goal vectors against the real verbs. With
// -update it (re)records the on-disk sample vectors from the verbs, so a
// deliberate verb change is a one-command re-record plus a reviewed diff.
func TestGoalsConformance(t *testing.T) {
	dir := vectorsDir()
	reg := goalsRegistry()

	if conf.Updating() {
		recordGoalVectors(t, dir)
	}
	conf.ReplayVectors(t, dir, reg, conf.WithSeed(seedGoal))
}

// recordGoalVectors writes the sample goal vectors from the registered verbs (the
// -update path). Each vector pins one verb call's transcript; the seed supplies
// the prior goal state the read-then-write verb needs.
func recordGoalVectors(t *testing.T, dir string) {
	t.Helper()
	const description = "goals: set a criterion to waiting-on-you — artifact.get, " +
		"artifact.update (CAS the goal with the criterion's status rewritten), then " +
		"message.publish a goal.update on msg.topic.goals."
	input, err := json.Marshal(goals.SetCriterionInput{
		GoalID:      "v0-5-0",
		CriterionID: "c1",
		Status:      goals.StatusWaitingOnYou,
		Headline:    "needs your sign-off",
		Ref:         "the-brief",
		By:          "01CREW",
	})
	if err != nil {
		t.Fatalf("marshal sample input: %v", err)
	}
	v, err := conf.RecordVector(1, "goals", "setCriterion", description, input, setCriterionVerb,
		func(rec *conf.Recorder) {
			seedGoal(rec, pconf.OpTranscriptVector{Verb: "setCriterion", Input: input})
		})
	if err != nil {
		t.Fatalf("record setCriterion: %v", err)
	}
	path := filepath.Join(dir, "goals", "setCriterion.json")
	if err := conf.WriteVector(path, v); err != nil {
		t.Fatalf("write setCriterion: %v", err)
	}
	t.Logf("re-recorded %s", path)
}
