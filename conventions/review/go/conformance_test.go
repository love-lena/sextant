package review_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	review "github.com/love-lena/sextant/conventions/review/go"
	pconf "github.com/love-lena/sextant/protocol/conformance"
	conf "github.com/love-lena/sextant/sdk/conformance"
)

// The review convention's conformance suite. It plugs the REAL review verb into
// the TASK-183 seam: register it in a conformance.Registry, then ReplayVectors over
// the language-neutral vectors under protocol/conformance/vectors/review — the SAME
// JSON files the TS conv-review suite replays (FORMAT.md, ADR-0041), so the two are
// co-equal (TASK-239 AC#2/AC#9). Two vectors pin the two transcripts: a plain
// verdict (get -> update) and an approve that runs the closed loop (get -> update ->
// goal.get -> goal.update -> publish goal.update).

// vectorsDir is the protocol-owned vector root, relative to this test
// (conventions/review/go -> repo root -> protocol/conformance/vectors).
func vectorsDir() string {
	return filepath.Join("..", "..", "..", "protocol", "conformance", "vectors")
}

// fixedNow is the verdict timestamp the recorded verb stamps, so the merged review
// block and any closed-loop goal.update are byte-stable across runs. The live
// SetReview takes the real time via SetReviewInput.Now; the recorded verb pins it.
const fixedNow = "2026-06-19T00:00:00Z"

// setReviewVerb adapts review.SetReview to a conformance.Verb: decode the vector's
// input and run the verb against the recorder (conf.Ops's method set is a superset
// of review.Ops, so the recorder is the verb's Ops unchanged).
func setReviewVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var in review.SetReviewInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("review conformance: decode setReview input: %w", err)
	}
	_, err := review.SetReview(ctx, ops, in)
	return err
}

func reviewRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register("review", "setReview", setReviewVerb)
	return reg
}

// seedReview seeds the recorder with the prior bus state a setReview vector reads
// before it writes. SetReview is a read-then-write verb (get the artifact, merge
// the review block, update); the seed mirrors the bus state it would find live and
// does not appear in the transcript. For an approve it also seeds the goal the
// artifact's proof relation backs, so the closed-loop transcript is recorded.
func seedReview(rec *conf.Recorder, v pconf.OpTranscriptVector) {
	if v.Verb != "setReview" {
		return
	}
	var in review.SetReviewInput
	_ = json.Unmarshal(v.Input, &in)
	if in.State == "approved" {
		// The approved artifact declares a proof relation backing goal g1 / crit c1,
		// at revision 2 (the value the artifact.update CAS's against).
		rec.SeedArtifact(in.Name, json.RawMessage(
			`{"$type":"doc","title":"PR #42","relates":[{"goal":"g1","crit":"c1","kind":"proof"}],"review":{"state":"review"}}`,
		), 2)
		// The goal it backs (c1 not yet met), at revision 5.
		rec.SeedArtifact("goal.g1", json.RawMessage(
			`{"$type":"goal","northstar":"ship it","criteria":[{"id":"c1","text":"merged","status":"in-progress"}]}`,
		), 5)
		return
	}
	// A plain verdict: a producer-marked doc at revision 3 (the CAS target).
	rec.SeedArtifact(in.Name, json.RawMessage(`{"$type":"doc","title":"the brief","body":"x","review":{"state":"review"}}`), 3)
}

// TestReviewConformance replays the review vectors against the real verb. With
// -update it (re)records the on-disk sample vectors, so a deliberate verb change is
// a one-command re-record plus a reviewed diff.
func TestReviewConformance(t *testing.T) {
	dir := vectorsDir()
	reg := reviewRegistry()

	if conf.Updating() {
		recordReviewVectors(t, dir)
	}
	conf.ReplayVectors(t, dir, reg, conf.WithSeed(seedReview))
}

// recordReviewVectors writes the sample review vectors from the registered verb
// (the -update path). Each vector pins one verb call's transcript; the seed
// supplies the prior bus state the read-then-write verb needs.
func recordReviewVectors(t *testing.T, dir string) {
	t.Helper()
	cases := []struct {
		file        string
		description string
		input       review.SetReviewInput
	}{
		{
			file: "setReview.json",
			description: "review: set a producer-marked doc to the 'changes' verdict — " +
				"artifact.get then artifact.update (CAS the record with the review block " +
				"merged in, every other field preserved). No closed loop (not an approve).",
			input: review.SetReviewInput{Name: "the-brief", State: "changes", By: "01OPERATOR", Now: fixedNow},
		},
		{
			file: "setReviewApprove.json",
			description: "review: approve a proof-bearing doc — artifact.get + artifact.update " +
				"persist the verdict, then the approve->met closed loop runs the goals " +
				"convention's single write path on the backed criterion: goal.get, " +
				"goal.update (CAS c1 to met), message.publish a goal.update on msg.topic.goals.",
			input: review.SetReviewInput{Name: "the-proof", State: "approved", By: "01OPERATOR", Now: fixedNow},
		},
	}
	for _, c := range cases {
		input, err := json.Marshal(c.input)
		if err != nil {
			t.Fatalf("marshal %s input: %v", c.file, err)
		}
		v, err := conf.RecordVector(1, "review", "setReview", c.description, input, setReviewVerb,
			func(rec *conf.Recorder) {
				seedReview(rec, pconf.OpTranscriptVector{Verb: "setReview", Input: input})
			})
		if err != nil {
			t.Fatalf("record %s: %v", c.file, err)
		}
		path := filepath.Join(dir, "review", c.file)
		if err := conf.WriteVector(path, v); err != nil {
			t.Fatalf("write %s: %v", c.file, err)
		}
		t.Logf("re-recorded %s", path)
	}
}
