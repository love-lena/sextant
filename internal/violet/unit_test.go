package violet

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/wire"
)

func TestPrefilterDropsNoiseKeepsSignal(t *testing.T) {
	cases := []struct {
		text     string
		operator bool
		want     bool
	}{
		{"still working on the dash fix", false, false}, // WIP noise
		{"agent.status heartbeat", false, false},        // churn
		{"the brief is ready for review", false, true},  // review-ready
		{"approved the release", false, true},           // verdict
		{"criterion went waiting-on-you", false, true},  // goal change
		{"merged #181 to v0.5", false, true},            // merged
		{"random chatter about lunch", false, false},    // noise
		{"random chatter about lunch", true, true},      // operator-authored always a candidate
		{"the GATE is open", false, true},               // case-insensitive
	}
	for _, c := range cases {
		if got := prefilter(c.text, c.operator); got != c.want {
			t.Errorf("prefilter(%q, op=%v) = %v, want %v", c.text, c.operator, got, c.want)
		}
	}
}

func TestTrimReplyEnforces250(t *testing.T) {
	short := "Goal v0.5 is at its gate — one brief waits on you: [[demo-brief]]."
	if got := trimReply(short); got != short {
		t.Errorf("short reply changed: %q", got)
	}
	long := strings.Repeat("a very long sentence that keeps going and going ", 20)
	got := trimReply(long)
	if len(got) > 251 { // 250 + the ellipsis rune may push the byte count by 2
		t.Errorf("trimReply did not cap length: %d chars", len(got))
	}
	// A reply with a sentence boundary in range trims cleanly to it.
	sentence := strings.Repeat("x", 210) + ". " + strings.Repeat("y", 100)
	if got := trimReply(sentence); !strings.HasSuffix(got, ".") || len(got) > 250 {
		t.Errorf("trimReply did not trim to sentence boundary: %q (len %d)", got, len(got))
	}
}

func TestGatherWorkspaceClassifies(t *testing.T) {
	fake := &fakeArtifactReader{
		infos: []artifactInfo{
			{Name: "the-brief", Revision: 3},
			{Name: "goal.v0-5-0", Revision: 5},
			{Name: "working-notes", Revision: 1},
			{Name: "approved-thing", Revision: 2},
		},
		records: map[string]json.RawMessage{
			"the-brief":      json.RawMessage(`{"title":"v0.5 demo brief","review":{"state":"review"}}`),
			"goal.v0-5-0":    json.RawMessage(`{"title":"v0.5.0","criteria":[{"label":"violet responds fast","status":"waiting-on-you"},{"label":"dash redesign","status":"met"}]}`),
			"working-notes":  json.RawMessage(`{"body":"scratch"}`),
			"approved-thing": json.RawMessage(`{"title":"done","review":{"state":"approved"}}`),
		},
	}
	ws, err := gatherWorkspace(context.Background(), fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.reviewQueue) != 1 || ws.reviewQueue[0].Name != "the-brief" {
		t.Fatalf("review queue = %+v, want [the-brief]", ws.reviewQueue)
	}
	if len(ws.goals) != 1 || len(ws.goals[0].Criteria) != 2 {
		t.Fatalf("goals = %+v, want 1 goal with 2 criteria", ws.goals)
	}
	if ws.goals[0].Criteria[0].Status != "waiting-on-you" {
		t.Fatalf("first criterion status = %q", ws.goals[0].Criteria[0].Status)
	}
	// working-notes + approved-thing are not review candidates → other.
	if ws.otherCount != 2 {
		t.Fatalf("otherCount = %d, want 2 (working-notes + approved-thing)", ws.otherCount)
	}

	// The curated home emits an agenda block first (ranked real calls) then a pinned block.
	proj := curateHome(ws)
	if len(proj.Blocks) < 2 {
		t.Fatalf("home blocks = %d, want >=2 (agenda + pinned)", len(proj.Blocks))
	}
	if proj.Blocks[0].Type != "agenda" || len(proj.Blocks[0].Items) == 0 {
		t.Fatalf("first block is not a non-empty agenda: %+v", proj.Blocks[0])
	}
	if proj.Blocks[0].Items[0].Ref != "the-brief" {
		t.Fatalf("agenda first item ref = %q, want the-brief", proj.Blocks[0].Items[0].Ref)
	}
	var pinned *homeBlock
	for i := range proj.Blocks {
		if proj.Blocks[i].Type == "pinned" {
			pinned = &proj.Blocks[i]
			break
		}
	}
	if pinned == nil || pinned.Names[0] != "the-brief" {
		t.Fatalf("pinned block missing or wrong: %+v", proj.Blocks)
	}
	if !strings.Contains(proj.Greeting.Note, "1 real call") {
		t.Fatalf("greeting note = %q, want a 1-real-call state line", proj.Greeting.Note)
	}
}

func TestRenderForCurationIncludesGoalAndQueue(t *testing.T) {
	ws := gatheredWorkspace{
		reviewQueue: []reviewItem{{Name: "brief-x", Revision: 2, State: "review", Title: "Brief X"}},
		goals:       []goalDigest{{Name: "g1", Headline: "Goal one", Criteria: []criterionDigest{{Label: "c1", Status: "waiting-on-you"}}}},
		otherCount:  4,
	}
	out := ws.renderForCuration()
	for _, want := range []string{"brief-x", "Brief X", "g1", "waiting-on-you", "4"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderForCuration missing %q:\n%s", want, out)
		}
	}
}

func TestHomeRankingByDownstreamBlocking(t *testing.T) {
	// item-b blocks 2 criteria; item-a blocks 0; item-c blocks 1.
	// Expected order: item-b first, then item-c, then item-a.
	ws := gatheredWorkspace{
		reviewQueue: []reviewItem{
			{Name: "item-a", State: "review"}, // no proof relations
			{Name: "item-b", State: "review", Relates: []relateEntry{
				{Goal: "v1", Crit: "crit1", Kind: "proof"},
				{Goal: "v1", Crit: "crit2", Kind: "proof"},
			}},
			{Name: "item-c", State: "review", Relates: []relateEntry{
				{Goal: "v1", Crit: "crit3", Kind: "proof"},
			}},
		},
		goals: []goalDigest{{Name: "goal.v1", Headline: "v1.0 milestone"}},
	}
	proj := curateHome(ws)
	var agenda *homeBlock
	for i := range proj.Blocks {
		if proj.Blocks[i].Type == "agenda" {
			agenda = &proj.Blocks[i]
			break
		}
	}
	if agenda == nil {
		t.Fatal("curateHome produced no agenda block")
	}
	if len(agenda.Items) != 3 {
		t.Fatalf("agenda items = %d, want 3", len(agenda.Items))
	}
	if agenda.Items[0].Ref != "item-b" {
		t.Errorf("first item = %q, want item-b (blocks 2 criteria)", agenda.Items[0].Ref)
	}
	if agenda.Items[1].Ref != "item-c" {
		t.Errorf("second item = %q, want item-c (blocks 1 criterion)", agenda.Items[1].Ref)
	}
	if agenda.Items[2].Ref != "item-a" {
		t.Errorf("third item = %q, want item-a (blocks 0 criteria)", agenda.Items[2].Ref)
	}
	if !strings.Contains(agenda.Items[0].Text, "item-b") {
		t.Errorf("first item why text does not mention item-b: %q", agenda.Items[0].Text)
	}
	if !strings.Contains(agenda.Items[0].Text, "2 criteria") {
		t.Errorf("first item why text does not quantify 2 criteria: %q", agenda.Items[0].Text)
	}
}

func TestHomeAgendaItemHasStructuredWhy(t *testing.T) {
	ws := gatheredWorkspace{
		reviewQueue: []reviewItem{
			{
				Name:  "my-brief",
				State: "review",
				Title: "My Brief",
				Relates: []relateEntry{
					{Goal: "goal1", Crit: "criterion-A", Kind: "proof"},
				},
			},
		},
		goals: []goalDigest{{Name: "goal.goal1", Headline: "Goal One"}},
	}
	proj := curateHome(ws)
	var agenda *homeBlock
	for i := range proj.Blocks {
		if proj.Blocks[i].Type == "agenda" {
			agenda = &proj.Blocks[i]
			break
		}
	}
	if agenda == nil || len(agenda.Items) == 0 {
		t.Fatal("no agenda items")
	}
	item := agenda.Items[0]
	if item.Text == "" {
		t.Error("agenda item has no why text (text field empty)")
	}
	if item.Ref != "my-brief" {
		t.Errorf("item ref = %q, want my-brief", item.Ref)
	}
	if item.Tone == "" {
		t.Error("agenda item has no tone")
	}
	if !strings.Contains(item.Text, "criterion-A") && !strings.Contains(item.Text, "Goal One") {
		t.Errorf("why text does not reference criterion or goal: %q", item.Text)
	}
}

// The mobilizer publishes the spawn.request shape the dispatcher consumes, under
// violet's own creds, and never hands out a credential.
func TestMobilizerSpawnPublishesRequest(t *testing.T) {
	pub := &capturePublisher{}
	m := &busMobilizer{pub: pub, self: "01VIOLET", spawnSubject: "msg.topic.spawn"}
	id, err := m.SpawnAgent(context.Background(), SpawnSpec{Prompt: "gather requirements", Nickname: "scout", Job: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "frame-1" {
		t.Fatalf("request id = %q", id)
	}
	if pub.lastSubject != "msg.topic.spawn" {
		t.Fatalf("spawn subject = %q", pub.lastSubject)
	}
	var rec map[string]any
	if err := json.Unmarshal(pub.lastRecord, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["$type"] != "spawn.request" || rec["prompt"] != "gather requirements" || rec["parent"] != "01VIOLET" {
		t.Fatalf("spawn record = %v (want spawn.request, prompt, parent=violet)", rec)
	}
	// No creds anywhere in the record — violet hands out no identity (TASK-158).
	if strings.Contains(string(pub.lastRecord), "creds") || strings.Contains(string(pub.lastRecord), "BEGIN NATS") {
		t.Fatalf("spawn record leaked credential material: %s", pub.lastRecord)
	}
}

func TestMobilizerSpawnRequiresPrompt(t *testing.T) {
	m := &busMobilizer{pub: &capturePublisher{}, self: "v", spawnSubject: "s"}
	if _, err := m.SpawnAgent(context.Background(), SpawnSpec{}); err == nil {
		t.Error("expected an error spawning with no prompt")
	}
}

func TestMobilizerStartWorkflow(t *testing.T) {
	pub := &capturePublisher{}
	m := &busMobilizer{pub: pub, self: "01VIOLET", wfSubject: "msg.topic.workflow.start"}
	if _, err := m.StartWorkflow(context.Background(), WorkflowSpec{PlanRef: "plan-a", Note: "kick off"}); err != nil {
		t.Fatal(err)
	}
	if pub.lastSubject != "msg.topic.workflow.start" {
		t.Fatalf("workflow subject = %q", pub.lastSubject)
	}
	var rec map[string]any
	_ = json.Unmarshal(pub.lastRecord, &rec)
	if rec["$type"] != "workflow.start" || rec["planRef"] != "plan-a" || rec["by"] != "01VIOLET" {
		t.Fatalf("workflow record = %v", rec)
	}
}

func TestWarmContextSwapAndPlaceholder(t *testing.T) {
	w := newWarmContext()
	snap, gen := w.get()
	if !strings.Contains(snap, "no workspace snapshot yet") || gen != 0 {
		t.Fatalf("initial = %q gen %d", snap, gen)
	}
	w.set("goal v0.5 at gate; one brief waits")
	snap, gen = w.get()
	if snap != "goal v0.5 at gate; one brief waits" || gen != 1 {
		t.Fatalf("after set = %q gen %d", snap, gen)
	}
	// An empty set must not blank the context the answers depend on.
	w.set("")
	snap, gen = w.get()
	if gen != 1 || snap == "" {
		t.Fatalf("empty set changed context: %q gen %d", snap, gen)
	}
}

func TestDMSubjectOrderIndependent(t *testing.T) {
	a := dmSubject("01AAA", "01ZZZ")
	b := dmSubject("01ZZZ", "01AAA")
	if a != b {
		t.Fatalf("dmSubject not order-independent: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "msg.topic.dm.") {
		t.Fatalf("dmSubject not under the dm namespace: %q", a)
	}
}

// --- fakes ---

type fakeArtifactReader struct {
	infos   []artifactInfo
	records map[string]json.RawMessage
}

func (f *fakeArtifactReader) ListArtifacts(context.Context) ([]artifactInfo, error) {
	return f.infos, nil
}

func (f *fakeArtifactReader) GetArtifact(_ context.Context, name string) (artifactValue, error) {
	rec := f.records[name]
	return artifactValue{Name: name, Record: rec}, nil
}

type capturePublisher struct {
	lastSubject string
	lastRecord  json.RawMessage
	n           int
}

func (c *capturePublisher) PublishMsg(_ context.Context, subject string, record json.RawMessage) (publishResult, error) {
	c.n++
	c.lastSubject = subject
	c.lastRecord = record
	return publishResult{ID: "frame-" + itoa(c.n)}, nil
}

var _ wire.Lexicon // keep the wire import honest if curateHome marshal changes

// TestVioletMobilizeSeam confirms the action surface is reachable through a live
// Violet and publishes under violet's own identity (parent=self) on the bus —
// the v1 cold-start seam: violet can mobilize work with no persistent crew, and
// hands out no credentials (the dispatcher mints the scoped child identity).
func TestVioletMobilizeSeam(t *testing.T) {
	bus := newFakeBus("01VIOLET", "01OPERATOR")
	v := New(bus, NewModelClient("k", "http://unused", nil), Config{OperatorID: "01OPERATOR", Logf: func(string, ...any) {}})

	id, err := v.Mobilize().SpawnAgent(context.Background(), SpawnSpec{Prompt: "gather requirements for the new feature"})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("spawn returned no request id")
	}
	rec, ok := bus.awaitPublish("msg.topic.spawn", time.Second)
	if !ok {
		t.Fatal("spawn.request was not published on the dispatcher subject")
	}
	if !strings.Contains(string(rec), `"$type":"spawn.request"`) || !strings.Contains(string(rec), `"parent":"01VIOLET"`) {
		t.Fatalf("spawn record not authored by violet: %s", rec)
	}

	// Boundary: the Mobilizer surface has ONLY start-work methods — no decide
	// methods (merge/approve/verdict/foreign-write). This is a compile-time
	// guarantee (the interface), reasserted here as documentation of intent.
	var _ interface {
		SpawnAgent(context.Context, SpawnSpec) (string, error)
		StartWorkflow(context.Context, WorkflowSpec) (string, error)
	} = v.Mobilize()
}
