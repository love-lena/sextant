//go:build e2e

package violet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
	"github.com/love-lena/sextant/clients/go/conventions/goals"
	sextant "github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// TestAC7_DashAndVioletRenderTheSameGoal is the TASK-173 operability proof (AC#7),
// driven on a throwaway HERMETIC bus — a real in-process bus (bus.Start), real SDK
// clients, the real dash HTTP serving + write path (dashapi.Server), and violet's
// real read half — never the operator's live bus/context. It sets a goal criterion
// through the dash WRITE path and confirms the dash's Goals data and violet's
// curated Home render the SAME north-star and the SAME criteria text/status.
//
// This is the end-to-end form of the consolidation: one goal artifact on a real
// bus, read by both halves through conv/goals, rendering identically. Before
// TASK-173 the two halves disagreed on field names; here they cannot, because they
// share one Goal type sourced from the lexicon.
func TestAC7_DashAndVioletRenderTheSameGoal(t *testing.T) {
	ctx := t.Context()

	// --- a throwaway, loopback-bound, in-process bus (torn down at test end) ---
	b, err := bus.Start(ctx, bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	dashCreds, _, err := b.MintClient(ctx, "dash", "human")
	if err != nil {
		t.Fatalf("MintClient(dash): %v", err)
	}
	violetCreds, _, err := b.MintClient(ctx, "violet", "agent")
	if err != nil {
		t.Fatalf("MintClient(violet): %v", err)
	}

	dashClient := connect(t, ctx, b.ClientURL(), dashCreds)
	violetClient := connect(t, ctx, b.ClientURL(), violetCreds)

	// --- seed a goal on the bus (canonical lexicon shape) plus a proof artifact ---
	// The dash WRITE path is exercised below by approving the proof, which flips the
	// proof's referenced criterion to met (closeLoop → goals.SetCriterion). We start
	// c1 in-progress so the approve is a real transition; c2 stays waiting-on-you so
	// both a met and a non-met criterion render.
	const goalID = "v0-5-0"
	goalRecord := `{"northstar":"Ship the goals convention",` +
		`"criteria":[` +
		`{"id":"c1","text":"both halves consume conv/goals","status":"in-progress","owner":"sirius"},` +
		`{"id":"c2","text":"the field-drift bug is gone","status":"waiting-on-you"}]}`
	if _, err := dashClient.CreateArtifact(ctx, goals.ArtifactName(goalID), wire.Lexicon(goalRecord)); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	proofRecord := `{"$type":"document","title":"the proof PR","body":"c1 is done",` +
		`"relates":[{"goal":"` + goalID + `","crit":"c1","kind":"proof"}],` +
		`"review":{"state":"review"}}`
	if _, err := dashClient.CreateArtifact(ctx, "the-proof", wire.Lexicon(proofRecord)); err != nil {
		t.Fatalf("create proof: %v", err)
	}

	// --- THE DASH: serve the API, and drive its real write path (approve → met) ---
	dashSrv := dashapi.New(dashapi.Config{Bus: dashClient, Token: "tok"})
	ts := httptest.NewServer(dashSrv)
	t.Cleanup(ts.Close)

	// Approve the proof through the real review endpoint: this runs closeLoop, which
	// calls goals.SetCriterion to flip c1 → met and announce it on msg.topic.goals.
	resp := post(t, ts.URL+"/api/artifacts/the-proof/review", `{"state":"approved"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve proof = %d", resp.StatusCode)
	}

	// Read the goal back the way the dash UI does: GET /api/artifacts/goal.<id>. The
	// dash JS (goals.jsx) renders northstar + criteria[].{text,status} straight off
	// this record, so the served record IS the dash's render data.
	dashGoal := getGoal(t, ts.URL+"/api/artifacts/"+goals.ArtifactName(goalID))

	// --- VIOLET: its real read half over the same bus -------------------------
	ws, err := gatherWorkspace(ctx, NewSDKAdapter(violetClient))
	if err != nil {
		t.Fatalf("violet gatherWorkspace: %v", err)
	}
	var violetGoal *goalDigest
	for i := range ws.goals {
		if ws.goals[i].Name == goals.ArtifactName(goalID) {
			violetGoal = &ws.goals[i]
			break
		}
	}
	if violetGoal == nil {
		t.Fatalf("violet did not surface the goal; goals=%+v", ws.goals)
	}

	// --- the proof: dash and violet render the SAME goal ----------------------
	// North-star: the dash serves it as `northstar`; violet's headline IS the
	// north-star (no `title` fallback). They must match.
	if dashGoal.Northstar != violetGoal.Headline {
		t.Errorf("north-star disagrees: dash %q vs violet %q", dashGoal.Northstar, violetGoal.Headline)
	}
	if dashGoal.Northstar != "Ship the goals convention" {
		t.Errorf("north-star = %q, want the seeded value", dashGoal.Northstar)
	}

	// Criteria: same count, same text and same effective status per criterion. The
	// dash serves stored statuses; violet applies the proof-filter (a met criterion
	// reads met only with proof). c1 is met WITH the-proof backing it, so both read
	// met; c2 reads waiting-on-you in both.
	if len(dashGoal.Criteria) != len(violetGoal.Criteria) {
		t.Fatalf("criteria count: dash %d vs violet %d", len(dashGoal.Criteria), len(violetGoal.Criteria))
	}
	provedByDash := goals.ProvedCriteria(goalID, []json.RawMessage{json.RawMessage(proofRecord)})
	for i, dc := range dashGoal.Criteria {
		vc := violetGoal.Criteria[i]
		if dc.Text != vc.Text {
			t.Errorf("criterion %d text disagrees: dash %q vs violet %q", i, dc.Text, vc.Text)
		}
		// The dash JS derives the displayed status through the same proof rule; here
		// we apply that rule to the dash's served record to compare like with like.
		dashEffective := goals.EffectiveStatus(dc, provedByDash)
		if dashEffective != vc.Status {
			t.Errorf("criterion %d status disagrees: dash %q vs violet %q", i, dashEffective, vc.Status)
		}
	}
	if dashGoal.Criteria[0].Status != goals.StatusMet {
		t.Errorf("dash served c1 status = %q, want met (the dash write path flipped it)", dashGoal.Criteria[0].Status)
	}
	if violetGoal.Criteria[0].Status != goals.StatusMet {
		t.Errorf("violet read c1 status = %q, want met (proved by the-proof)", violetGoal.Criteria[0].Status)
	}

	// Evidence for the AC#7 record: the two renders, side by side.
	t.Logf("AC#7 dash Goals render:   northstar=%q criteria=%s", dashGoal.Northstar, criteriaLine(dashGoal.Criteria))
	t.Logf("AC#7 violet Home render:  northstar=%q criteria=%s", violetGoal.Headline, violetCriteriaLine(violetGoal.Criteria))
	t.Logf("AC#7 violet curated Home text:\n%s", ws.renderForCuration())
}

func connect(t *testing.T, ctx context.Context, url, creds string) *sextant.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), strings.ReplaceAll(t.Name(), "/", "_")+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	c, err := sextant.Connect(ctx, sextant.Options{URL: url, CredsPath: path, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// getGoal reads a goal artifact through the dash API and parses its record the way
// the dash UI renders it — into the canonical Goal type. The dash serves the raw
// record under {name, record, revision}.
func getGoal(t *testing.T, url string) goals.Goal {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	var got struct {
		Record json.RawMessage `json:"record"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode goal: %v", err)
	}
	g, ok := goals.ParseGoal(got.Record)
	if !ok {
		t.Fatalf("dash served a non-goal record: %s", got.Record)
	}
	return g
}

func criteriaLine(cs []goals.Criterion) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("[" + c.Status + "] " + c.Text + "  ")
	}
	return b.String()
}

func violetCriteriaLine(cs []criterionDigest) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("[" + c.Status + "] " + c.Text + "  ")
	}
	return b.String()
}
