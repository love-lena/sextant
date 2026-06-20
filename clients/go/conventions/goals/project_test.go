package goals_test

import (
	"encoding/json"
	"testing"

	"github.com/love-lena/sextant/clients/go/conventions/goals"
)

// TestProjectAppliesProofFilter is the proof that the Goals projection turns a
// stored goal into effective statuses in ONE place: an unproved stored "met"
// projects as in-progress and is NOT counted in the rollup; a proved "met"
// projects met and counts. This is the rule a UI must NOT reimplement.
func TestProjectAppliesProofFilter(t *testing.T) {
	arts := []goals.Artifact{
		{
			Name:     "goal.g1",
			Revision: 7,
			Record: json.RawMessage(`{"$type":"goal","northstar":"ship it","stream":"v0.5",` +
				`"criteria":[` +
				`{"id":"c1","text":"proved done","status":"met","owner":"sirius"},` + // proved below
				`{"id":"c2","text":"claimed done, no proof","status":"met"},` + // UNPROVED met
				`{"id":"c3","text":"in flight","status":"in-progress"}],` +
				`"review":{"state":"review"}}`),
		},
		// A proof artifact backing only c1.
		{Name: "the-proof", Revision: 1, Record: json.RawMessage(`{"title":"PR","relates":[{"goal":"g1","crit":"c1","kind":"proof"}]}`)},
		// A soft related ref to c2 — does NOT prove it.
		{Name: "a-note", Revision: 1, Record: json.RawMessage(`{"title":"note","relates":[{"goal":"g1","crit":"c2","kind":"related"}]}`)},
	}

	views := goals.Project(arts)
	if len(views) != 1 {
		t.Fatalf("Project returned %d views, want 1", len(views))
	}
	g := views[0]
	if g.ID != "g1" || g.Name != "goal.g1" || g.Northstar != "ship it" || g.Stream != "v0.5" {
		t.Errorf("view header wrong: %+v", g)
	}
	if g.Revision != 7 {
		t.Errorf("revision = %d, want 7", g.Revision)
	}
	if g.Review != "review" {
		t.Errorf("review = %q, want review", g.Review)
	}
	if len(g.Criteria) != 3 {
		t.Fatalf("criteria = %d, want 3", len(g.Criteria))
	}

	// c1: proved met → reads met, carries the proof evidence.
	if g.Criteria[0].Status != goals.StatusMet {
		t.Errorf("c1 effective status = %q, want met (proved)", g.Criteria[0].Status)
	}
	if len(g.Criteria[0].Evidence) != 1 || g.Criteria[0].Evidence[0].Kind != "proof" || g.Criteria[0].Evidence[0].Name != "the-proof" {
		t.Errorf("c1 evidence = %+v, want [the-proof proof]", g.Criteria[0].Evidence)
	}
	// c2: UNPROVED met → reads in-progress (the proof-filter downgrade), soft
	// related evidence present but not counted as proof.
	if g.Criteria[1].Status != goals.StatusInProgress {
		t.Errorf("c2 effective status = %q, want in-progress (unproved met downgraded)", g.Criteria[1].Status)
	}
	if len(g.Criteria[1].Evidence) != 1 || g.Criteria[1].Evidence[0].Kind != "related" {
		t.Errorf("c2 evidence = %+v, want [a-note related]", g.Criteria[1].Evidence)
	}

	// Rollup: only c1 counts as met (1 of 3), derived after the filter.
	if g.Rollup.Met != 1 || g.Rollup.Total != 3 {
		t.Errorf("rollup = %+v, want Met=1 Total=3 (the unproved met does NOT count)", g.Rollup)
	}
}

func TestProjectIgnoresNonGoals(t *testing.T) {
	arts := []goals.Artifact{
		{Name: "a-doc", Record: json.RawMessage(`{"title":"not a goal","body":"x"}`)},
		{Name: "goal.g2", Record: json.RawMessage(`{"northstar":"y","criteria":[]}`)},
	}
	views := goals.Project(arts)
	if len(views) != 1 || views[0].ID != "g2" {
		t.Fatalf("Project should surface only goal.g2; got %+v", views)
	}
}
