package dashapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// reviewBus is a fake holding one document artifact to exercise the review
// convention (POST /api/artifacts/{name}/review, TASK-66/TASK-71).
func reviewBus() *fakeBus {
	return &fakeBus{
		id: "01OPERATOR",
		artifact: map[string]sextant.Artifact{
			"brief": {Name: "brief", Record: wire.Lexicon(`{"$type":"document","title":"Brief","body":"hello"}`), Revision: 3},
		},
	}
}

func postReview(t *testing.T, srv http.Handler, name, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/artifacts/"+name+"/review", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestArtifactReviewSetsStateInRecord: POST .../review merges a review block into
// the artifact's record (CAS-bumping the revision), stamps who set it, and leaves
// the original document fields intact.
func TestArtifactReviewSetsStateInRecord(t *testing.T) {
	bus := reviewBus()
	rec := postReview(t, newServer(bus, "tok"), "brief", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := bus.artifact["brief"]
	if got.Revision != 4 {
		t.Fatalf("revision = %d, want 4 (CAS bump)", got.Revision)
	}
	var rmap map[string]json.RawMessage
	if err := json.Unmarshal(got.Record, &rmap); err != nil {
		t.Fatalf("record not an object: %v", err)
	}
	if !strings.Contains(string(rmap["review"]), `"approved"`) {
		t.Fatalf("review state not persisted: %s", got.Record)
	}
	if !strings.Contains(string(rmap["review"]), "01OPERATOR") {
		t.Fatalf("review.by not stamped with the dash identity: %s", got.Record)
	}
	if !strings.Contains(string(rmap["review"]), `"rev":3`) {
		t.Fatalf("review.rev not recorded against the approved revision (3): %s", got.Record)
	}
	if !strings.Contains(string(rmap["$type"]), "document") || !strings.Contains(string(rmap["body"]), "hello") {
		t.Fatalf("original record fields clobbered: %s", got.Record)
	}
}

// TestArtifactReviewAcceptsArchiveAndReject: the terminal states are valid.
func TestArtifactReviewAcceptsArchiveAndReject(t *testing.T) {
	for _, st := range []string{"archived", "rejected"} {
		rec := postReview(t, newServer(reviewBus(), "tok"), "brief", `{"state":"`+st+`"}`, "tok")
		if rec.Code != http.StatusOK {
			t.Fatalf("state %q: status = %d, want 200 (%s)", st, rec.Code, rec.Body.String())
		}
	}
}

// TestArtifactReviewRejectsUnknownState: only the convention's states are allowed.
func TestArtifactReviewRejectsUnknownState(t *testing.T) {
	rec := postReview(t, newServer(reviewBus(), "tok"), "brief", `{"state":"banana"}`, "tok")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestArtifactReviewRequiresToken: the review write is gated like every /api call.
func TestArtifactReviewRequiresToken(t *testing.T) {
	rec := postReview(t, newServer(reviewBus(), "tok"), "brief", `{"state":"approved"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestArtifactReviewMissingArtifactIs404: reviewing an artifact that isn't there
// is a 404, not a silent create.
func TestArtifactReviewMissingArtifactIs404(t *testing.T) {
	rec := postReview(t, newServer(reviewBus(), "tok"), "ghost", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestArtifactReviewRetriesOnConflict: a single compare-and-set conflict is retried
// and the second attempt persists.
func TestArtifactReviewRetriesOnConflict(t *testing.T) {
	bus := reviewBus()
	bus.failUpdates = 1
	rec := postReview(t, newServer(bus, "tok"), "brief", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after a one-conflict retry (%s)", rec.Code, rec.Body.String())
	}
	if bus.artifact["brief"].Revision != 4 {
		t.Fatalf("revision = %d, want 4 (retry persisted)", bus.artifact["brief"].Revision)
	}
}

// TestArtifactReviewReports502OnPersistentFailure: exhausting the retry budget is a 502.
func TestArtifactReviewReports502OnPersistentFailure(t *testing.T) {
	bus := reviewBus()
	bus.failUpdates = 5 // more than the handler's retry budget
	rec := postReview(t, newServer(bus, "tok"), "brief", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 after exhausting retries", rec.Code)
	}
}

// TestArtifactReviewRejectsNonObjectRecord: a record that isn't a JSON object is a 422,
// so the review merge never silently drops content.
func TestArtifactReviewRejectsNonObjectRecord(t *testing.T) {
	bus := &fakeBus{id: "01ME", artifact: map[string]sextant.Artifact{
		"weird": {Name: "weird", Record: wire.Lexicon(`"just a string"`), Revision: 1},
	}}
	rec := postReview(t, newServer(bus, "tok"), "weird", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for a non-object record", rec.Code)
	}
}

// --- decision-earned closed loop (goals-design D3) -------------------------
//
// Approving a proof artifact (relates[].kind=="proof") flips the referenced
// goal criterion to "met" and emits a goal.update — a dash-client convenience
// over the bus primitives (the goal.<id> artifact + msg.topic.goals stream),
// not a core change. It is best-effort: a closed-loop failure never turns the
// approve into an error.

// loopBus seeds a fake with a proof artifact that relates to goal g1's criterion
// c1, plus the goal.g1 artifact itself (c1 in-progress, c2 not-started). relates
// is the artifact-side handle the dash reads; the relate's kind/crit drive what
// the loop does.
func loopBus(relates string) *fakeBus {
	return &fakeBus{
		id: "01OPERATOR",
		artifact: map[string]sextant.Artifact{
			"proof": {
				Name:     "proof",
				Record:   wire.Lexicon(`{"$type":"document","title":"Proof","body":"done","relates":` + relates + `}`),
				Revision: 3,
			},
			"goal.g1": {
				Name:     "goal.g1",
				Record:   wire.Lexicon(`{"northstar":"ship v0.5","criteria":[{"id":"c1","text":"do c1","status":"in-progress"},{"id":"c2","text":"do c2","status":"not-started"}],"updated":"2026-01-01T00:00:00Z","by":"01CREW"}`),
				Revision: 7,
			},
		},
	}
}

// criterionStatus returns the status of criterion critID in the goal.<gid>
// artifact, or "" if absent. A test helper for reading the closed-loop outcome.
func criterionStatus(t *testing.T, bus *fakeBus, gid, critID string) string {
	t.Helper()
	art, ok := bus.artifact["goal."+gid]
	if !ok {
		t.Fatalf("goal.%s artifact missing", gid)
	}
	var rec struct {
		Criteria []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(art.Record, &rec); err != nil {
		t.Fatalf("goal.%s record not parseable: %v", gid, err)
	}
	for _, c := range rec.Criteria {
		if c.ID == critID {
			return c.Status
		}
	}
	return ""
}

// goalUpdates returns every goal.update published on msg.topic.goals.
func goalUpdates(bus *fakeBus) []map[string]any {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	var out []map[string]any
	for _, p := range bus.published {
		if p.subject != "msg.topic.goals" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(p.record, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Test 1: approving a proof relate flips the criterion to met, emits a
// goal.update on msg.topic.goals, leaves the sibling criterion untouched, and
// still returns 200.
func TestArtifactReviewClosedLoopFlipsAndEmits(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "met" {
		t.Fatalf("c1 status = %q, want met", got)
	}
	if got := criterionStatus(t, bus, "g1", "c2"); got != "not-started" {
		t.Fatalf("c2 status = %q, want not-started (sibling untouched)", got)
	}
	ups := goalUpdates(bus)
	if len(ups) != 1 {
		t.Fatalf("goal.update count = %d, want 1 (%v)", len(ups), ups)
	}
	u := ups[0]
	if u["goal"] != "g1" || u["crit"] != "c1" || u["status"] != "met" {
		t.Fatalf("goal.update = %v, want goal=g1 crit=c1 status=met", u)
	}
	if u["ref"] != "proof" {
		t.Fatalf("goal.update ref = %v, want the approved artifact name 'proof'", u["ref"])
	}
	if u["$type"] != "goal.update" {
		t.Fatalf("goal.update $type = %v, want goal.update", u["$type"])
	}
}

// Test 2: a non-proof relate (kind=="related") does not run the loop.
func TestArtifactReviewClosedLoopIgnoresNonProof(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"related"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "in-progress" {
		t.Fatalf("c1 status = %q, want in-progress (non-proof must not flip)", got)
	}
	if n := len(goalUpdates(bus)); n != 0 {
		t.Fatalf("goal.update count = %d, want 0 for a non-proof relate", n)
	}
}

// Test 3: the proof relates to a goal whose goal.<id> artifact is absent. The
// approve still succeeds, nothing is published, and nothing panics.
func TestArtifactReviewClosedLoopMissingGoalIsBestEffort(t *testing.T) {
	bus := loopBus(`[{"goal":"gX","crit":"c1","kind":"proof"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when the goal artifact is missing (%s)", rec.Code, rec.Body.String())
	}
	if n := len(goalUpdates(bus)); n != 0 {
		t.Fatalf("goal.update count = %d, want 0 when the goal artifact is missing", n)
	}
}

// Test 4: a criterion already "met" is idempotent — no re-write, no re-emit.
func TestArtifactReviewClosedLoopIdempotent(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	// Seed c1 already met.
	bus.artifact["goal.g1"] = sextant.Artifact{
		Name:     "goal.g1",
		Record:   wire.Lexicon(`{"northstar":"ship v0.5","criteria":[{"id":"c1","text":"do c1","status":"met"},{"id":"c2","text":"do c2","status":"not-started"}],"updated":"2026-01-01T00:00:00Z","by":"01CREW"}`),
		Revision: 7,
	}
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if bus.artifact["goal.g1"].Revision != 7 {
		t.Fatalf("goal.g1 revision = %d, want 7 (no re-write for an already-met criterion)", bus.artifact["goal.g1"].Revision)
	}
	if n := len(goalUpdates(bus)); n != 0 {
		t.Fatalf("goal.update count = %d, want 0 (idempotent)", n)
	}
}

// Test 5: a verdict other than "approved" never runs the loop, even with a proof
// relate present.
func TestArtifactReviewClosedLoopOnlyOnApprove(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"changes"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "in-progress" {
		t.Fatalf("c1 status = %q, want in-progress (changes must not flip)", got)
	}
	if n := len(goalUpdates(bus)); n != 0 {
		t.Fatalf("goal.update count = %d, want 0 for a non-approve verdict", n)
	}
}

// Test 6: a proof relate with a goal but no crit has nothing to flip — no
// criterion change, no publish.
func TestArtifactReviewClosedLoopProofWithoutCritSkips(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","kind":"proof"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "in-progress" {
		t.Fatalf("c1 status = %q, want in-progress (no crit ⇒ nothing to flip)", got)
	}
	if n := len(goalUpdates(bus)); n != 0 {
		t.Fatalf("goal.update count = %d, want 0 for a proof with no crit", n)
	}
}

// Test 7: a CAS conflict on the GOAL write is retried once (re-get + reapply),
// then persists. failGoalUpdate targets only goal.* writes, so the verdict write
// succeeds cleanly and the conflict is injected exactly at the closed-loop's goal
// write — which the global failUpdates counter could not do (it would trip the
// verdict write first).
func TestArtifactReviewClosedLoopRetriesGoalCAS(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	bus.failGoalUpdate = 1
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "met" {
		t.Fatalf("c1 status = %q, want met after a one-conflict retry on the goal write", got)
	}
	if n := len(goalUpdates(bus)); n != 1 {
		t.Fatalf("goal.update count = %d, want 1 after the retried goal write", n)
	}
}

// Test 8: the flip changes ONLY the matched criterion's status — every other
// field of the goal record (northstar, updated, by, sibling criteria, and the
// flipped criterion's own text) survives the round-trip. Guards setCriterionMet
// against a future refactor silently dropping or rewriting fields.
func TestArtifactReviewClosedLoopPreservesGoalFields(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var g struct {
		Northstar string `json:"northstar"`
		Updated   string `json:"updated"`
		By        string `json:"by"`
		Criteria  []struct {
			ID, Text, Status string
		} `json:"criteria"`
	}
	if err := json.Unmarshal(bus.artifact["goal.g1"].Record, &g); err != nil {
		t.Fatalf("goal.g1 record not parseable: %v", err)
	}
	if g.Northstar != "ship v0.5" || g.Updated != "2026-01-01T00:00:00Z" || g.By != "01CREW" {
		t.Fatalf("top-level fields not preserved: northstar=%q updated=%q by=%q", g.Northstar, g.Updated, g.By)
	}
	if len(g.Criteria) != 2 {
		t.Fatalf("criteria count = %d, want 2 (none dropped)", len(g.Criteria))
	}
	c1, c2 := g.Criteria[0], g.Criteria[1]
	if c1.ID != "c1" || c1.Text != "do c1" || c1.Status != "met" {
		t.Fatalf("c1 = %+v, want id=c1 text='do c1' status=met (only status changed)", c1)
	}
	if c2.ID != "c2" || c2.Text != "do c2" || c2.Status != "not-started" {
		t.Fatalf("c2 = %+v, want fully untouched", c2)
	}
}

// Test 9: a publish failure on the goal.update is best-effort — the goal write
// already landed, so the criterion stays met and the approve still returns 200
// (the announcement is allowed to fail without demoting the verdict).
func TestArtifactReviewClosedLoopPublishFailureIsBestEffort(t *testing.T) {
	bus := loopBus(`[{"goal":"g1","crit":"c1","kind":"proof"}]`)
	bus.publishErr = errors.New("bus unavailable")
	rec := postReview(t, newServer(bus, "tok"), "proof", `{"state":"approved"}`, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 despite the publish failure (%s)", rec.Code, rec.Body.String())
	}
	if got := criterionStatus(t, bus, "g1", "c1"); got != "met" {
		t.Fatalf("c1 status = %q, want met — the goal write precedes (and is independent of) the publish", got)
	}
}
