package spawn_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	spawn "github.com/love-lena/sextant/conventions/spawn/go"
	conf "github.com/love-lena/sextant/sdk/conformance"
)

// The spawn convention's conformance suite. It plugs the REAL RequestSpawn verb
// into the TASK-183 seam and ReplayVectors over the language-neutral vectors under
// protocol/conformance/vectors/spawn — the SAME JSON the TS conv-spawn suite
// replays (FORMAT.md, ADR-0041), so the two are co-equal (TASK-239 AC#8/AC#9). The
// verb is publish-only, so the transcript is a single message.publish — no seed.

func vectorsDir() string {
	return filepath.Join("..", "..", "..", "protocol", "conformance", "vectors")
}

// requestSpawnVerb adapts spawn.RequestSpawn to a conformance.Verb: decode the
// vector's input as a spawn.request and publish it on the default RequestSubject
// (conf.Ops's method set is a superset of spawn.Ops, so the recorder is the verb's
// Ops unchanged).
func requestSpawnVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var req spawn.SpawnRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return fmt.Errorf("spawn conformance: decode requestSpawn input: %w", err)
	}
	return spawn.RequestSpawn(ctx, ops, req, spawn.RequestSubject)
}

func spawnRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register("spawn", "requestSpawn", requestSpawnVerb)
	return reg
}

// TestSpawnConformance replays the spawn vectors against the real verb. With
// -update it (re)records the on-disk sample vector from the verb.
func TestSpawnConformance(t *testing.T) {
	dir := vectorsDir()
	reg := spawnRegistry()

	if conf.Updating() {
		recordSpawnVectors(t, dir)
	}
	conf.ReplayVectors(t, dir, reg)
}

// recordSpawnVectors writes the sample spawn vector from the registered verb (the
// -update path). The input is the minimal {prompt, nickname} case, so the vector
// pins that the empty lineage fields (job/parent) are omitted from the wire record.
func recordSpawnVectors(t *testing.T, dir string) {
	t.Helper()
	const description = "spawn: request a dispatcher to spawn an agent — a single " +
		"message.publish of a spawn.request on msg.topic.spawn. Optional lineage " +
		"(job/parent) is omitted when unset; parent is never injected (the dispatcher " +
		"trusts the bus-stamped author)."
	input, err := json.Marshal(struct {
		Prompt   string `json:"prompt"`
		Nickname string `json:"nickname"`
	}{Prompt: "say hello on msg.topic.demo", Nickname: "alpha"})
	if err != nil {
		t.Fatalf("marshal sample input: %v", err)
	}
	v, err := conf.RecordVector(1, "spawn", "requestSpawn", description, input, requestSpawnVerb, nil)
	if err != nil {
		t.Fatalf("record requestSpawn: %v", err)
	}
	path := filepath.Join(dir, "spawn", "requestSpawn.json")
	if err := conf.WriteVector(path, v); err != nil {
		t.Fatalf("write requestSpawn: %v", err)
	}
	t.Logf("re-recorded %s", path)
}
