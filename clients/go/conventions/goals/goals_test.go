package goals_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/love-lena/sextant/clients/go/conventions/goals"
)

// fakeOps is a minimal in-memory Ops: one seeded goal artifact plus a captured
// publish, enough to exercise SetCriterion's get→update→publish without a bus.
type fakeOps struct {
	record   json.RawMessage
	revision uint64
	getErr   error
	updErr   error

	updated     json.RawMessage
	updatedRev  uint64
	published   json.RawMessage
	pubSubject  string
	updateCalls int
	pubCalls    int
}

func (f *fakeOps) GetArtifact(_ context.Context, _ string) (json.RawMessage, uint64, error) {
	return f.record, f.revision, f.getErr
}

func (f *fakeOps) UpdateArtifact(_ context.Context, _ string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	f.updateCalls++
	if f.updErr != nil {
		return 0, f.updErr
	}
	f.updated = record
	f.updatedRev = expectedRev
	return expectedRev + 1, nil
}

func (f *fakeOps) Publish(_ context.Context, subject string, record json.RawMessage) error {
	f.pubCalls++
	f.pubSubject = subject
	f.published = record
	return nil
}

const sampleGoal = `{"$type":"goal","northstar":"Ship the goals convention","criteria":[` +
	`{"id":"c1","text":"types generated from the lexicon","status":"in-progress","owner":"sirius"},` +
	`{"id":"c2","text":"both halves consume conv/goals","status":"not-started"}]}`

func TestSetCriterionWritesAndAnnounces(t *testing.T) {
	f := &fakeOps{record: json.RawMessage(sampleGoal), revision: 4}
	changed, err := goals.SetCriterion(context.Background(), f, goals.SetCriterionInput{
		GoalID:      "g1",
		CriterionID: "c1",
		Status:      goals.StatusWaitingOnYou,
		Headline:    "needs your eyes",
		Ref:         "the-pr",
	}, "2026-06-19T00:00:00Z")
	if err != nil {
		t.Fatalf("SetCriterion: %v", err)
	}
	if !changed {
		t.Fatal("changed=false, want true")
	}
	// The update CAS'd against the get's revision and rewrote c1's status, leaving
	// c2 and the north-star untouched.
	if f.updatedRev != 4 {
		t.Errorf("update expectedRev = %d, want 4", f.updatedRev)
	}
	g, ok := goals.ParseGoal(f.updated)
	if !ok {
		t.Fatalf("updated record is not a goal: %s", f.updated)
	}
	if g.Northstar != "Ship the goals convention" {
		t.Errorf("north-star not preserved: %q", g.Northstar)
	}
	if g.Criteria[0].Status != goals.StatusWaitingOnYou {
		t.Errorf("c1 status = %q, want waiting-on-you", g.Criteria[0].Status)
	}
	if g.Criteria[0].Text != "types generated from the lexicon" || g.Criteria[0].Owner != "sirius" {
		t.Errorf("c1 text/owner not preserved: %+v", g.Criteria[0])
	}
	if g.Criteria[1].Status != goals.StatusNotStarted {
		t.Errorf("c2 status changed: %q", g.Criteria[1].Status)
	}
	// The announcement went out on the goals topic as a goal.update.
	if f.pubSubject != goals.GoalsSubject {
		t.Errorf("publish subject = %q, want %q", f.pubSubject, goals.GoalsSubject)
	}
	var up goals.Update
	if err := json.Unmarshal(f.published, &up); err != nil {
		t.Fatalf("goal.update unmarshal: %v", err)
	}
	if up.Type != "goal.update" || up.Goal != "g1" || up.Crit != "c1" || up.Status != goals.StatusWaitingOnYou {
		t.Errorf("goal.update = %+v", up)
	}
}

func TestSetCriterionIdempotent(t *testing.T) {
	f := &fakeOps{record: json.RawMessage(sampleGoal), revision: 4}
	// c1 is already in-progress; setting it to in-progress is a no-op.
	changed, err := goals.SetCriterion(context.Background(), f, goals.SetCriterionInput{
		GoalID: "g1", CriterionID: "c1", Status: goals.StatusInProgress, Headline: "x",
	}, "")
	if err != nil {
		t.Fatalf("SetCriterion: %v", err)
	}
	if changed {
		t.Error("changed=true on a no-op set")
	}
	if f.updateCalls != 0 || f.pubCalls != 0 {
		t.Errorf("no-op set still wrote/announced: update=%d publish=%d", f.updateCalls, f.pubCalls)
	}
}

func TestSetCriterionAbsentCriterion(t *testing.T) {
	f := &fakeOps{record: json.RawMessage(sampleGoal), revision: 4}
	changed, err := goals.SetCriterion(context.Background(), f, goals.SetCriterionInput{
		GoalID: "g1", CriterionID: "nope", Status: goals.StatusMet, Headline: "x",
	}, "")
	if err != nil {
		t.Fatalf("SetCriterion: %v", err)
	}
	if changed || f.updateCalls != 0 {
		t.Errorf("absent criterion should be a no-op: changed=%v update=%d", changed, f.updateCalls)
	}
}

// --- the proof-filter and the derived rollup ---

func TestProofFilterMetNeedsProof(t *testing.T) {
	// A criterion stored "met" with no proof reads as in-progress; with a proof it
	// reads met — the single met-needs-proof invariant.
	c := goals.Criterion{ID: "c1", Text: "done", Status: goals.StatusMet}
	if goals.CriterionMet(c, nil) {
		t.Error("met with no proof: CriterionMet=true, want false")
	}
	if got := goals.EffectiveStatus(c, nil); got != goals.StatusInProgress {
		t.Errorf("unproved met reads as %q, want in-progress", got)
	}
	proved := map[string]bool{"c1": true}
	if !goals.CriterionMet(c, proved) {
		t.Error("met WITH proof: CriterionMet=false, want true")
	}
	if got := goals.EffectiveStatus(c, proved); got != goals.StatusMet {
		t.Errorf("proved met reads as %q, want met", got)
	}
}

func TestProvedCriteriaFromArtifacts(t *testing.T) {
	// Two artifacts relate to g1: one a proof for c1, one a soft "related" for c2.
	// Only the proof counts.
	proofArt := json.RawMessage(`{"title":"the pr","relates":[{"goal":"g1","crit":"c1","kind":"proof"}]}`)
	softArt := json.RawMessage(`{"title":"a note","relates":[{"goal":"g1","crit":"c2","kind":"related"}]}`)
	proved := goals.ProvedCriteria("g1", []json.RawMessage{proofArt, softArt})
	if !proved["c1"] {
		t.Error("c1 should be proved (a proof artifact relates to it)")
	}
	if proved["c2"] {
		t.Error("c2 should not be proved (only a soft relation)")
	}
}

func TestRollupDerivesStatus(t *testing.T) {
	g := goals.Goal{
		Northstar: "ship it",
		Criteria: []goals.Criterion{
			{ID: "c1", Status: goals.StatusMet},          // proved below → met
			{ID: "c2", Status: goals.StatusMet},          // NOT proved → in-progress
			{ID: "c3", Status: goals.StatusWaitingOnYou}, // waiting
			{ID: "c4", Status: goals.StatusBlocked},      // blocked
		},
	}
	r := g.Rollup(map[string]bool{"c1": true})
	if r.Total != 4 {
		t.Errorf("Total = %d, want 4", r.Total)
	}
	if r.Met != 1 {
		t.Errorf("Met = %d, want 1 (only c1 is proved)", r.Met)
	}
	if r.Waiting != 1 {
		t.Errorf("Waiting = %d, want 1", r.Waiting)
	}
	if !r.Blocked {
		t.Error("Blocked = false, want true")
	}
	if !r.Defined {
		t.Error("Defined = false, want true (has north-star + criteria)")
	}
}

func TestRollupUndefined(t *testing.T) {
	if (goals.Goal{Northstar: "x"}).Rollup(nil).Defined {
		t.Error("a goal with no criteria reads as Defined")
	}
	if (goals.Goal{Criteria: []goals.Criterion{{ID: "c1"}}}).Rollup(nil).Defined {
		t.Error("a goal with no north-star reads as Defined")
	}
}
