package dashapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
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
