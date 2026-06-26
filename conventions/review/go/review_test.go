package review_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	goals "github.com/love-lena/sextant/conventions/goal/go"
	review "github.com/love-lena/sextant/conventions/review/go"
)

const now = "2026-06-19T00:00:00Z"

// storeOps is a multi-artifact in-memory Ops (the Go peer of the TS StoreOps): a
// name→{record,revision} map plus a captured publish log, enough to exercise
// SetReview's CAS AND the closed loop's separate goal.<id> write without a bus.
type storeOps struct {
	arts      map[string]artState
	published []publishedMsg
	// conflictOnce makes the first UpdateArtifact for a name fail (a lost CAS), so
	// the retry path is exercised.
	conflictOnce map[string]bool
	updateCalls  map[string]int
}

type artState struct {
	record   json.RawMessage
	revision uint64
}

type publishedMsg struct {
	subject string
	record  json.RawMessage
}

func newStore() *storeOps {
	return &storeOps{
		arts:         map[string]artState{},
		conflictOnce: map[string]bool{},
		updateCalls:  map[string]int{},
	}
}

func (s *storeOps) seed(name string, record json.RawMessage, rev uint64) {
	s.arts[name] = artState{record: record, revision: rev}
}

func (s *storeOps) GetArtifact(_ context.Context, name string) (json.RawMessage, uint64, error) {
	a, ok := s.arts[name]
	if !ok {
		return nil, 0, errors.New("not found: " + name)
	}
	return a.record, a.revision, nil
}

func (s *storeOps) UpdateArtifact(_ context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	s.updateCalls[name]++
	a, ok := s.arts[name]
	if !ok {
		return 0, errors.New("not found: " + name)
	}
	if s.conflictOnce[name] {
		delete(s.conflictOnce, name)
		a.revision++ // a concurrent write moved the revision
		s.arts[name] = a
		return 0, errors.New("revision conflict")
	}
	if a.revision != expectedRev {
		return 0, errors.New("revision conflict")
	}
	a.record = record
	a.revision++
	s.arts[name] = a
	return a.revision, nil
}

func (s *storeOps) Publish(_ context.Context, subject string, record json.RawMessage) error {
	s.published = append(s.published, publishedMsg{subject: subject, record: record})
	return nil
}

// objField unmarshals record into a map and returns the raw value of key.
func objField(t *testing.T, record json.RawMessage, key string) json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(record, &m); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	return m[key]
}

func strField(t *testing.T, record json.RawMessage, key string) string {
	t.Helper()
	var s string
	if raw := objField(t, record, key); raw != nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func TestSetReviewPersistsVerdictPreservingOtherFields(t *testing.T) {
	ops := newStore()
	ops.seed("brief", json.RawMessage(`{"$type":"doc","title":"the brief","body":"x","review":{"state":"review"}}`), 3)

	res, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "brief", State: "approved", By: "01OPERATOR", Now: now})
	if err != nil {
		t.Fatalf("SetReview: %v", err)
	}
	if res.Review != "approved" || res.Revision != 4 {
		t.Fatalf("result = %+v, want review=approved revision=4", res)
	}

	after := ops.arts["brief"].record
	if got := strField(t, after, "title"); got != "the brief" {
		t.Errorf("title preserved: got %q", got)
	}
	if got := strField(t, after, "body"); got != "x" {
		t.Errorf("body preserved: got %q", got)
	}
	rb := objField(t, after, "review")
	if got := strField(t, rb, "state"); got != "approved" {
		t.Errorf("review.state = %q, want approved", got)
	}
	if got := strField(t, rb, "by"); got != "01OPERATOR" {
		t.Errorf("review.by = %q", got)
	}
	if got := strField(t, rb, "at"); got != now {
		t.Errorf("review.at = %q", got)
	}
	var rev uint64
	_ = json.Unmarshal(objField(t, rb, "rev"), &rev)
	if rev != 3 {
		t.Errorf("review.rev = %d, want 3 (the revision the verdict was made against)", rev)
	}
}

func TestSetReviewRetriesOnceOnCASConflict(t *testing.T) {
	ops := newStore()
	ops.seed("brief", json.RawMessage(`{"$type":"doc","review":{"state":"review"}}`), 1)
	ops.conflictOnce["brief"] = true

	res, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "brief", State: "changes", By: "01OP", Now: now})
	if err != nil {
		t.Fatalf("SetReview: %v", err)
	}
	if res.Review != "changes" {
		t.Errorf("review = %q, want changes", res.Review)
	}
	if ops.updateCalls["brief"] != 2 {
		t.Errorf("updateCalls = %d, want 2 (first failed, retry succeeded)", ops.updateCalls["brief"])
	}
}

func TestMergeReviewStateOnlyHasNoPhantomVerdict(t *testing.T) {
	merged, err := review.MergeReview(json.RawMessage(`{"$type":"doc","body":"y"}`), review.ReviewBlock{State: "review"})
	if err != nil {
		t.Fatalf("MergeReview: %v", err)
	}
	rb := objField(t, merged, "review")
	if got := strField(t, rb, "state"); got != "review" {
		t.Errorf("state = %q", got)
	}
	for _, k := range []string{"by", "at", "rev"} {
		if raw := objField(t, rb, k); raw != nil {
			t.Errorf("state-only block carries phantom %q = %s", k, raw)
		}
	}
}

func TestApproveRunsClosedLoop(t *testing.T) {
	ops := newStore()
	ops.seed("the-proof", json.RawMessage(`{"$type":"doc","title":"PR #42","relates":[{"goal":"g1","crit":"c1","kind":"proof"}],"review":{"state":"review"}}`), 2)
	ops.seed("goal.g1", json.RawMessage(`{"$type":"goal","northstar":"ship it","criteria":[{"id":"c1","text":"merged","status":"in-progress"}]}`), 5)

	res, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "the-proof", State: "approved", By: "01OPERATOR", Now: now})
	if err != nil {
		t.Fatalf("SetReview: %v", err)
	}
	if len(res.Advanced) != 1 || res.Advanced[0] != (review.AdvancedCrit{Goal: "g1", Crit: "c1"}) {
		t.Fatalf("advanced = %+v, want [{g1 c1}]", res.Advanced)
	}

	// The goal criterion moved to met.
	goal := ops.arts["goal.g1"].record
	var g struct {
		Criteria []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(goal, &g); err != nil {
		t.Fatalf("unmarshal goal: %v", err)
	}
	if g.Criteria[0].Status != goals.StatusMet {
		t.Errorf("criterion status = %q, want %q", g.Criteria[0].Status, goals.StatusMet)
	}

	// A goal.update was announced on the goals topic.
	var announce *publishedMsg
	for i := range ops.published {
		if ops.published[i].subject == goals.GoalsSubject {
			announce = &ops.published[i]
		}
	}
	if announce == nil {
		t.Fatal("no goal.update published on msg.topic.goals")
	}
	if got := strField(t, announce.record, "goal"); got != "g1" {
		t.Errorf("announce goal = %q", got)
	}
	if got := strField(t, announce.record, "status"); got != goals.StatusMet {
		t.Errorf("announce status = %q, want met", got)
	}
}

func TestClosedLoopBestEffortOnMissingGoal(t *testing.T) {
	ops := newStore()
	ops.seed("the-proof", json.RawMessage(`{"$type":"doc","relates":[{"goal":"ghost","crit":"c9","kind":"proof"}],"review":{"state":"review"}}`), 1)
	// goal.ghost is NOT seeded — the closed loop's get fails, swallowed.
	res, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "the-proof", State: "approved", By: "01OP", Now: now})
	if err != nil {
		t.Fatalf("SetReview should not fail on a closed-loop miss: %v", err)
	}
	if res.Review != "approved" {
		t.Errorf("review = %q, want approved", res.Review)
	}
	if len(res.Advanced) != 0 {
		t.Errorf("advanced = %+v, want empty", res.Advanced)
	}
}

func TestNonApproveDoesNotRunClosedLoop(t *testing.T) {
	ops := newStore()
	ops.seed("the-proof", json.RawMessage(`{"$type":"doc","relates":[{"goal":"g1","crit":"c1","kind":"proof"}],"review":{"state":"review"}}`), 1)
	ops.seed("goal.g1", json.RawMessage(`{"$type":"goal","northstar":"x","criteria":[{"id":"c1","text":"t","status":"in-progress"}]}`), 1)

	res, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "the-proof", State: "changes", By: "01OP", Now: now})
	if err != nil {
		t.Fatalf("SetReview: %v", err)
	}
	if len(res.Advanced) != 0 {
		t.Errorf("advanced = %+v, want empty (non-approve)", res.Advanced)
	}
	var g struct {
		Criteria []struct {
			Status string `json:"status"`
		} `json:"criteria"`
	}
	_ = json.Unmarshal(ops.arts["goal.g1"].record, &g)
	if g.Criteria[0].Status != "in-progress" {
		t.Errorf("goal criterion changed to %q on a non-approve", g.Criteria[0].Status)
	}
}

func TestSetReviewRejectsUnknownState(t *testing.T) {
	ops := newStore()
	ops.seed("brief", json.RawMessage(`{"$type":"doc"}`), 1)
	_, err := review.SetReview(context.Background(), ops, review.SetReviewInput{Name: "brief", State: "bogus", By: "x", Now: now})
	if !errors.Is(err, review.ErrReview) {
		t.Errorf("err = %v, want ErrReview for an unknown state", err)
	}
}

func TestReadParsesReviewBlock(t *testing.T) {
	rb, ok := review.Read(json.RawMessage(`{"$type":"doc","review":{"state":"approved","by":"01OP","at":"t","rev":7}}`))
	if !ok {
		t.Fatal("Read returned ok=false for a record with a review block")
	}
	if rb.State != "approved" || rb.By != "01OP" || rb.Rev != 7 {
		t.Errorf("Read = %+v", rb)
	}
	if _, ok := review.Read(json.RawMessage(`{"$type":"doc","body":"x"}`)); ok {
		t.Error("Read returned ok=true for a record with no review block")
	}
}
