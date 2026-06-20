package dashapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
	"github.com/love-lena/sextant/clients/go/conventions/goals"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// TestGoalsEndpointAppliesProofFilter pins the FIX 1 contract: GET /api/goals
// serves the goal read-model with conv/goals' proof-filter ALREADY APPLIED
// server-side. An unproved stored "met" criterion is served as "in-progress" and
// is NOT counted in the rollup; a proved "met" is served met. The dash JS renders
// this verbatim — it does not (must not) re-derive the proof rule. This is the
// regression that would have caught the dash↔violet divergence the review found.
func TestGoalsEndpointAppliesProofFilter(t *testing.T) {
	bus := &fakeBus{
		id: "01OPERATOR",
		artifacts: []sextant.ArtifactInfo{
			{Name: "goal.g1", Revision: 5},
			{Name: "the-proof", Revision: 1},
		},
		artifact: map[string]sextant.Artifact{
			"goal.g1": {Name: "goal.g1", Revision: 5, Record: wire.Lexicon(
				`{"$type":"goal","northstar":"ship it","criteria":[` +
					`{"id":"c1","text":"proved done","status":"met"},` + // proved by the-proof → reads met
					`{"id":"c2","text":"claimed, no proof","status":"met"}]}`,
			)}, // UNPROVED → reads in-progress
			"the-proof": {Name: "the-proof", Revision: 1, Record: wire.Lexicon(
				`{"$type":"document","relates":[{"goal":"g1","crit":"c1","kind":"proof"}]}`,
			)},
		},
	}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok"})

	req := httptest.NewRequest(http.MethodGet, "/api/goals", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/goals = %d: %s", rec.Code, rec.Body)
	}

	var views []goals.GoalView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode goals projection: %v\n%s", err, rec.Body)
	}
	if len(views) != 1 {
		t.Fatalf("projection has %d goals, want 1", len(views))
	}
	g := views[0]
	if g.Northstar != "ship it" {
		t.Errorf("northstar = %q, want \"ship it\"", g.Northstar)
	}
	if len(g.Criteria) != 2 {
		t.Fatalf("criteria = %d, want 2", len(g.Criteria))
	}
	// THE PROOF: the served statuses are effective, not raw.
	if g.Criteria[0].Status != "met" {
		t.Errorf("c1 served status = %q, want met (proved by the-proof)", g.Criteria[0].Status)
	}
	if g.Criteria[1].Status != "in-progress" {
		t.Errorf("c2 served status = %q, want in-progress (unproved met downgraded server-side)", g.Criteria[1].Status)
	}
	if g.Rollup.Met != 1 || g.Rollup.Total != 2 {
		t.Errorf("rollup = %+v, want Met=1 Total=2 (the unproved met is NOT counted)", g.Rollup)
	}
	// The proof evidence is wired onto c1 so the UI can show the chip.
	if len(g.Criteria[0].Evidence) != 1 || g.Criteria[0].Evidence[0].Name != "the-proof" {
		t.Errorf("c1 evidence = %+v, want [the-proof]", g.Criteria[0].Evidence)
	}
}

// TestGoalsEndpointRequiresToken: the projection is gated like every /api call.
func TestGoalsEndpointRequiresToken(t *testing.T) {
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01ME"}, Token: "tok"})
	req := httptest.NewRequest(http.MethodGet, "/api/goals", nil)
	req.RemoteAddr = "203.0.113.7:9999" // a non-loopback peer, so the token gate applies
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/goals without token = %d, want 401", rec.Code)
	}
}
