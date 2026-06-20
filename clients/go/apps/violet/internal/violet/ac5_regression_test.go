package violet

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/love-lena/sextant/clients/go/conventions/goals"
)

// TestAC5_DashWriteVioletReadAgree is the TASK-173 regression test (AC#5): the
// dash WRITE path sets a goal criterion, and violet's READER sees the same
// criterion's text and status — with NO field-name fallback. It is the proof that
// the label/state field-drift bug is fixed at the root: both halves now consume
// conv/goals' one Goal type, so they cannot disagree about a field name. Before
// the fix, violet read criteria off `label`/`state` and the headline off `title`,
// while the dash wrote `text`/`status`/`northstar` — the fallback masked the drift.
//
// The dash write path is goals.SetCriterion (the convention's single write verb,
// which the dash's approve→met loop drives); the violet read path is
// gatherWorkspace → digestGoal. Both run against one in-memory store, so what the
// dash writes is exactly what violet reads.
func TestAC5_DashWriteVioletReadAgree(t *testing.T) {
	store := newGoalStore()
	// A goal on the bus: a north-star plus two criteria, the canonical lexicon
	// shape. (northstar/text/status — never title/label/state.)
	store.put("goal.v0-5-0", `{"northstar":"Ship the goals convention","criteria":[`+
		`{"id":"c1","text":"both halves consume conv/goals","status":"in-progress","owner":"sirius"},`+
		`{"id":"c2","text":"the field-drift bug is gone","status":"not-started"}]}`)

	// THE DASH WRITE PATH: set criterion c1 to waiting-on-you via goals.SetCriterion.
	changed, err := goals.SetCriterion(context.Background(), store, goals.SetCriterionInput{
		GoalID:      "v0-5-0",
		CriterionID: "c1",
		Status:      goals.StatusWaitingOnYou,
		Headline:    "needs your sign-off",
	}, "2026-06-19T00:00:00Z")
	if err != nil {
		t.Fatalf("dash write (SetCriterion): %v", err)
	}
	if !changed {
		t.Fatal("SetCriterion reported no change")
	}

	// THE VIOLET READ PATH: gather the workspace and find the goal's digest.
	ws, err := gatherWorkspace(context.Background(), readerOf(store))
	if err != nil {
		t.Fatalf("violet read (gatherWorkspace): %v", err)
	}
	if len(ws.goals) != 1 {
		t.Fatalf("violet read %d goals, want 1", len(ws.goals))
	}
	g := ws.goals[0]

	// The headline is the north-star (NOT a title fallback — there is no title).
	if g.Headline != "Ship the goals convention" {
		t.Errorf("violet headline = %q, want the north-star (no title fallback)", g.Headline)
	}
	if len(g.Criteria) != 2 {
		t.Fatalf("violet read %d criteria, want 2", len(g.Criteria))
	}

	// The criterion the dash wrote: violet sees the SAME text and status. Read the
	// canonical record back so the assertion is "what was written == what was read"
	// on the exact lexicon fields, with no fallback in between.
	wrote, ok := goals.ParseGoal(store.get("goal.v0-5-0"))
	if !ok {
		t.Fatal("stored goal is not parseable")
	}
	wroteC1 := wrote.Criteria[0]
	readC1 := g.Criteria[0]
	if readC1.Text != wroteC1.Text {
		t.Errorf("text disagrees: dash wrote %q, violet read %q", wroteC1.Text, readC1.Text)
	}
	if readC1.Status != wroteC1.Status {
		t.Errorf("status disagrees: dash wrote %q, violet read %q", wroteC1.Status, readC1.Status)
	}
	if readC1.Text != "both halves consume conv/goals" || readC1.Status != goals.StatusWaitingOnYou {
		t.Errorf("violet read c1 = {%q, %q}, want {the lexicon text, waiting-on-you}", readC1.Text, readC1.Status)
	}
}

// goalStore is a tiny in-memory artifact store. It satisfies the dash's write
// surface (goals.Ops) directly; readerOf wraps it as violet's read surface
// (artifactReader) — two surfaces over ONE store, so a record written by the dash
// path is read by the violet path. (One type can't satisfy both directly: both
// name a GetArtifact method but with different return shapes.) The publish side is
// a no-op; violet reads the artifact, not the goal.update stream.
type goalStore struct {
	records map[string]json.RawMessage
	revs    map[string]uint64
}

func newGoalStore() *goalStore {
	return &goalStore{records: map[string]json.RawMessage{}, revs: map[string]uint64{}}
}

func (s *goalStore) put(name, record string) {
	s.records[name] = json.RawMessage(record)
	s.revs[name] = 1
}

func (s *goalStore) get(name string) json.RawMessage { return s.records[name] }

// --- goals.Ops (the dash write surface) ---

func (s *goalStore) GetArtifact(_ context.Context, name string) (json.RawMessage, uint64, error) {
	return s.records[name], s.revs[name], nil
}

func (s *goalStore) UpdateArtifact(_ context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	s.records[name] = record
	s.revs[name] = expectedRev + 1
	return s.revs[name], nil
}

func (s *goalStore) Publish(_ context.Context, _ string, _ json.RawMessage) error { return nil }

// --- artifactReader (the violet read surface), via a wrapper over the same store ---

// storeReader wraps a goalStore as violet's artifactReader. The wrapper exists
// only because artifactReader's GetArtifact returns artifactValue while goals.Ops'
// GetArtifact returns (raw, rev) — same name, different shape, so they can't both
// hang off goalStore.
type storeReader struct{ s *goalStore }

func readerOf(s *goalStore) storeReader { return storeReader{s: s} }

func (r storeReader) ListArtifacts(_ context.Context) ([]artifactInfo, error) {
	out := make([]artifactInfo, 0, len(r.s.records))
	for name, rev := range r.s.revs {
		out = append(out, artifactInfo{Name: name, Revision: rev})
	}
	return out, nil
}

func (r storeReader) GetArtifact(_ context.Context, name string) (artifactValue, error) {
	return artifactValue{Name: name, Record: r.s.records[name], Revision: r.s.revs[name]}, nil
}

// Compile-time proof that the store satisfies the dash write surface and the
// wrapper satisfies the violet read surface.
var (
	_ goals.Ops      = (*goalStore)(nil)
	_ artifactReader = storeReader{}
)
