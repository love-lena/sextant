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
	// Three criteria exercise the proof-filter end to end:
	//   c1 — starts in-progress; the dash WRITE path flips it to met (with proof),
	//   c2 — waiting-on-you, no proof,
	//   c3 — stored "met" but with NO proof relation: THE DIVERGENCE CASE. The raw
	//        record says met; the proof-filter must read it as in-progress. A render
	//        that counts raw status (the old dash JS) would show c3 "Met" while
	//        violet shows "in-progress" — the exact drift this ticket kills.
	const goalID = "v0-5-0"
	goalRecord := `{"northstar":"Ship the goals convention",` +
		`"criteria":[` +
		`{"id":"c1","text":"both halves consume conv/goals","status":"in-progress","owner":"sirius"},` +
		`{"id":"c2","text":"the field-drift bug is gone","status":"waiting-on-you"},` +
		`{"id":"c3","text":"claimed done with no proof","status":"met"}]}`
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

	// Read the goal the way the dash UI actually does: GET /api/goals — the SERVED
	// projection, with the proof-filter applied SERVER-SIDE (conv/goals). This is
	// the real render path the JS consumes; the JS does not re-derive status. We
	// assert on what the backend serves, not on a Go re-derivation of it.
	dashGoal := getServedGoal(t, ts.URL+"/api/goals", goals.ArtifactName(goalID))

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
	// north-star (no `title` fallback). They must match the seeded value.
	if dashGoal.Northstar != violetGoal.Headline {
		t.Errorf("north-star disagrees: dash %q vs violet %q", dashGoal.Northstar, violetGoal.Headline)
	}
	if dashGoal.Northstar != "Ship the goals convention" {
		t.Errorf("north-star = %q, want the seeded value", dashGoal.Northstar)
	}

	// Criteria: same count, same text, and same status per criterion — compared as
	// CONCRETE expected strings (no goals.EffectiveStatus(...)==goals.EffectiveStatus(...)
	// circularity). The dash status is the SERVED value; violet's is its digest.
	if len(dashGoal.Criteria) != 3 || len(violetGoal.Criteria) != 3 {
		t.Fatalf("criteria count: dash %d, violet %d, want 3 each", len(dashGoal.Criteria), len(violetGoal.Criteria))
	}
	wantStatus := []string{goals.StatusMet, goals.StatusWaitingOnYou, goals.StatusInProgress} // c3 met→in-progress (no proof)
	wantText := []string{"both halves consume conv/goals", "the field-drift bug is gone", "claimed done with no proof"}
	for i := range dashGoal.Criteria {
		dc, vc := dashGoal.Criteria[i], violetGoal.Criteria[i]
		if dc.Text != wantText[i] || vc.Text != wantText[i] {
			t.Errorf("criterion %d text: dash %q, violet %q, want %q", i, dc.Text, vc.Text, wantText[i])
		}
		if dc.Status != wantStatus[i] || vc.Status != wantStatus[i] {
			t.Errorf("criterion %d status: dash served %q, violet %q, want %q", i, dc.Status, vc.Status, wantStatus[i])
		}
	}

	// THE DIVERGENCE GUARD: c3 is stored "met" with NO proof. The dash SERVES it as
	// the concrete string "in-progress" (not "met"), and does NOT count it in the
	// met rollup — identical to violet. A raw-status render would fail right here.
	if got := dashGoal.Criteria[2].Status; got != "in-progress" {
		t.Errorf("c3 (unproved met) dash served status = %q, want \"in-progress\" (proof-filtered server-side)", got)
	}
	if got := violetGoal.Criteria[2].Status; got != "in-progress" {
		t.Errorf("c3 (unproved met) violet status = %q, want \"in-progress\"", got)
	}
	if dashGoal.Rollup.Met != 1 {
		t.Errorf("dash served rollup Met = %d, want 1 (c1 proved; c3's unproved met NOT counted)", dashGoal.Rollup.Met)
	}
	if dashGoal.Rollup.Total != 3 {
		t.Errorf("dash served rollup Total = %d, want 3", dashGoal.Rollup.Total)
	}

	// Evidence for the AC#7 record: the two renders, side by side.
	t.Logf("AC#7 dash Goals render (served):  northstar=%q rollup=%d/%d criteria=%s",
		dashGoal.Northstar, dashGoal.Rollup.Met, dashGoal.Rollup.Total, viewCriteriaLine(dashGoal.Criteria))
	t.Logf("AC#7 violet Home render:          northstar=%q criteria=%s", violetGoal.Headline, violetCriteriaLine(violetGoal.Criteria))
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

// getServedGoal reads the dash's GOALS PROJECTION (GET /api/goals) — the real
// render path the dash JS consumes — and returns the named goal's served view,
// with the proof-filter ALREADY APPLIED by the backend. The test asserts on these
// served values directly, so it catches a backend that failed to filter (rather
// than re-deriving the filter in Go and comparing it to itself).
func getServedGoal(t *testing.T, url, name string) goals.GoalView {
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
	var views []goals.GoalView
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("decode goals projection: %v", err)
	}
	for _, v := range views {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("dash projection did not include %s; got %+v", name, views)
	return goals.GoalView{}
}

func viewCriteriaLine(cs []goals.CriterionView) string {
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
