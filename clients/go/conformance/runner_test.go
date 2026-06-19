package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	conf "github.com/love-lena/sextant/clients/go/conformance"
	pconf "github.com/love-lena/sextant/protocol/conformance"
	"github.com/love-lena/sextant/protocol/wire"
)

// vectorsDir is the language-neutral vector root, relative to this test
// (clients/go/conformance → repo root → protocol/conformance/vectors). Every
// language's suite reads vectors from this same protocol-owned location.
func vectorsDir() string {
	return filepath.Join("..", "..", "..", "protocol", "conformance", "vectors")
}

func methodsPath() string {
	return filepath.Join("..", "..", "..", "protocol", "methods.json")
}

// --- The fixture verb: a tiny goals-like verb defined IN THE TEST ---
//
// It stands in for the real goals convention (which lands in TASK-173) so the
// machinery — record a verb, replay the vector, assert — is proven now, before
// conv/goals exists. Its shape mirrors what a real verb does: read nothing,
// write the goal artifact, then announce the change on the goals topic. It is
// written against conf.Ops, so the same function records (against a Recorder)
// and would run live (against SDKOps) unchanged.

type fixtureInput struct {
	GoalID    string `json:"goalId"`
	Northstar string `json:"northstar"`
}

// fixtureSetGoal is the fixture verb: an artifact.update on goal.<id> followed
// by a message.publish on the goals topic — the two-op pattern a real
// "set a goal" verb follows.
func fixtureSetGoal(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var in fixtureInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("fixture: decode input: %w", err)
	}
	if in.GoalID == "" {
		return fmt.Errorf("fixture: goalId required")
	}
	name := "goal." + in.GoalID
	record, _ := json.Marshal(map[string]any{
		"$type":     "goal",
		"northstar": in.Northstar,
		"criteria":  []any{},
	})
	if _, err := ops.UpdateArtifact(ctx, name, record, 0); err != nil {
		return err
	}
	update, _ := json.Marshal(map[string]any{
		"$type":  "goal.update",
		"goalId": in.GoalID,
		"kind":   "set",
	})
	return ops.Publish(ctx, "msg.topic.goals", update)
}

const (
	fixtureConvention = "fixture"
	fixtureVerb       = "setGoal"
)

func fixtureRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register(fixtureConvention, fixtureVerb, fixtureSetGoal)
	return reg
}

func fixtureSampleInput() json.RawMessage {
	return json.RawMessage(`{"goalId":"sample","northstar":"prove the conformance machinery"}`)
}

// TestFixtureVectorRoundTrips records the fixture verb, replays the recorded
// vector against the verb, and asserts they match — the end-to-end proof that
// recording and replay agree. It is self-contained (no file on disk) so it
// always runs; the on-disk sample vector (TestReplayVectors) proves the file
// path of the same machinery.
func TestFixtureVectorRoundTrips(t *testing.T) {
	v, err := conf.RecordVector(1, fixtureConvention, fixtureVerb,
		"fixture: set a goal — artifact.update then message.publish",
		fixtureSampleInput(), fixtureSetGoal, nil)
	if err != nil {
		t.Fatalf("record fixture vector: %v", err)
	}
	if len(v.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(v.Operations))
	}
	if v.Operations[0].Op != "artifact.update" || v.Operations[1].Op != "message.publish" {
		t.Fatalf("unexpected op sequence: %s, %s", v.Operations[0].Op, v.Operations[1].Op)
	}

	// Round-trip through JSON (what disk persistence does) then replay against a
	// fresh recorder: the captured ops must canonical-equal the recorded ones.
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded pconf.OpTranscriptVector
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatal(err)
	}
	rec := conf.NewRecorder()
	if err := fixtureSetGoal(context.Background(), rec, reloaded.Input); err != nil {
		t.Fatal(err)
	}
	if len(rec.Operations()) != len(reloaded.Operations) {
		t.Fatalf("replay op count %d != recorded %d", len(rec.Operations()), len(reloaded.Operations))
	}
	for i := range reloaded.Operations {
		eq, err := pconf.CanonicalEqual(reloaded.Operations[i].Payload, rec.Operations()[i].Payload)
		if err != nil {
			t.Fatal(err)
		}
		if !eq {
			t.Errorf("op[%d] payload diverged on round-trip", i)
		}
	}
}

// TestReplayVectors is the on-disk sample-vector test: it discovers the sample
// vector under protocol/conformance/vectors/fixture and replays it against the
// registered fixture verb. With -update it (re)records the sample. This is the
// exact path TASK-173 follows for conv/goals: register the verbs, drop vectors
// under protocol/conformance/vectors/goals, call ReplayVectors — no runner
// change.
func TestReplayVectors(t *testing.T) {
	dir := vectorsDir()
	reg := fixtureRegistry()

	if conf.Updating() {
		// Re-record the sample vector from the fixture verb so the on-disk file
		// always reflects current verb behaviour after a `-update` run.
		v, err := conf.RecordVector(1, fixtureConvention, fixtureVerb,
			"Sample fixture vector (TASK-183): a goals-like verb emits artifact.update then message.publish. Replaced by real conv/goals vectors in TASK-173.",
			fixtureSampleInput(), fixtureSetGoal, nil)
		if err != nil {
			t.Fatalf("record sample: %v", err)
		}
		path := filepath.Join(dir, fixtureConvention, fixtureVerb+".json")
		if err := conf.WriteVector(path, v); err != nil {
			t.Fatalf("write sample: %v", err)
		}
		t.Logf("re-recorded %s", path)
	}

	conf.ReplayVectors(t, dir, reg)
}

// TestVectorOpsAreDeclared subsumes the methods.json name-set check, extended to
// the transcripts: every op any vector names must be a real protocol operation.
func TestVectorOpsAreDeclared(t *testing.T) {
	conf.AssertVectorOpsInMethods(t, vectorsDir(), methodsPath())
}

// sampleWireFrame is a fixed, deterministic message frame (a fixed ULID, no
// random stamping) so the wire vector it produces is stable across runs — a
// vector with a fresh ULID each time would churn on every -update.
func sampleWireFrame() wire.Frame {
	return wire.Frame{
		ID:     "01J0000000000000000000FRAME",
		Author: "01J000000000000000000AUTHOR",
		Kind:   wire.KindMessage,
		Epoch:  wire.Epoch,
		Record: wire.Lexicon(`{"$type":"chat.message","text":"hello, bus"}`),
	}
}

// TestReplayWireVectors verifies the Go frame codec against the on-disk wire
// vector(s). With -update it (re)records the sample from the codec. TASK-174's
// TS codec replays these same files to prove byte-faithfulness across languages.
func TestReplayWireVectors(t *testing.T) {
	dir := vectorsDir()
	if conf.Updating() {
		v, err := conf.RecordWireVector(wire.Epoch,
			"Sample wire vector (TASK-183): a chat.message frame and its canonical JSON bytes. TASK-174's TS codec replays this for byte-faithfulness.",
			sampleWireFrame())
		if err != nil {
			t.Fatalf("record wire sample: %v", err)
		}
		path := filepath.Join(dir, "wire", "message-frame.json")
		if err := conf.WriteWireVector(path, v); err != nil {
			t.Fatalf("write wire sample: %v", err)
		}
		t.Logf("re-recorded %s", path)
	}
	conf.ReplayWireVectors(t, dir)
}
