//go:build e2e

package violet

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/sextant-dash/dashapi"
	"github.com/love-lena/sextant/conventions/goal/go"
	"github.com/love-lena/sextant/protocol/wire"
)

// TestAC5_DashWriteVioletReadAgree is the TASK-173 regression (AC#5), driven on a
// REAL hermetic bus through the dash's REAL write entrypoint — not an in-memory
// double. It is the focused proof of the consolidation: the dash write path sets a
// goal criterion, and violet's reader sees the SAME criterion's text and status,
// with NO field-name fallback.
//
// "The dash write path" here is the actual review endpoint: POST
// /api/artifacts/{name}/review {state:"approved"} on a proof artifact runs the
// dash's closeLoop, which drives goals.SetCriterion through the goalsOps adapter
// to flip the referenced criterion to met (CAS the goal.<id> artifact on the real
// bus + announce it). "violet's reader" is the production gatherWorkspace over the
// same bus. Both consume conv/goals' one Goal type, so a field-name disagreement
// (the label/state drift bug) is structurally impossible — this test would catch a
// regression of it.
func TestAC5_DashWriteVioletReadAgree(t *testing.T) {
	ctx := t.Context()

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

	// A goal with one in-progress criterion, plus a proof artifact relating to it.
	const goalID = "g1"
	const critText = "both halves consume conv/goals"
	goalRecord := `{"northstar":"Ship the goals convention","criteria":[` +
		`{"id":"c1","text":"` + critText + `","status":"in-progress","owner":"sirius"}]}`
	if _, err := dashClient.CreateArtifact(ctx, goals.ArtifactName(goalID), wire.Lexicon(goalRecord)); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	proofRecord := `{"$type":"document","title":"proof PR",` +
		`"relates":[{"goal":"` + goalID + `","crit":"c1","kind":"proof"}],"review":{"state":"review"}}`
	if _, err := dashClient.CreateArtifact(ctx, "the-proof", wire.Lexicon(proofRecord)); err != nil {
		t.Fatalf("create proof: %v", err)
	}

	// THE DASH WRITE PATH: approve the proof → closeLoop → goals.SetCriterion flips
	// c1 to met on the real bus.
	dashSrv := dashapi.New(dashapi.Config{Bus: dashClient, Token: "tok"})
	ts := httptest.NewServer(dashSrv)
	t.Cleanup(ts.Close)
	if resp := post(t, ts.URL+"/api/artifacts/the-proof/review", `{"state":"approved"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve = %d", resp.StatusCode)
	}

	// THE VIOLET READ PATH over the same bus.
	ws, err := gatherWorkspace(ctx, NewSDKAdapter(violetClient))
	if err != nil {
		t.Fatalf("violet gatherWorkspace: %v", err)
	}
	var g *goalDigest
	for i := range ws.goals {
		if ws.goals[i].Name == goals.ArtifactName(goalID) {
			g = &ws.goals[i]
			break
		}
	}
	if g == nil || len(g.Criteria) != 1 {
		t.Fatalf("violet did not surface the goal with 1 criterion; goals=%+v", ws.goals)
	}

	// What the dash WROTE, read straight off the goal.<id> artifact's canonical
	// fields (no fallback) — the source of truth the violet digest must match.
	art, err := dashClient.GetArtifact(ctx, goals.ArtifactName(goalID))
	if err != nil {
		t.Fatalf("get goal artifact: %v", err)
	}
	wrote, ok := goals.ParseGoal(wire.Lexicon(art.Record))
	if !ok || len(wrote.Criteria) != 1 {
		t.Fatalf("stored goal not parseable as a 1-criterion goal: %s", art.Record)
	}

	// The proof: violet's reader sees identical text+status, on the canonical lexicon
	// fields (text/status), with no label/state fallback. The dash flipped c1 to met
	// and a proof backs it, so violet reads met too.
	read := g.Criteria[0]
	if read.Text != wrote.Criteria[0].Text || read.Text != critText {
		t.Errorf("text: violet read %q, dash wrote %q, want %q", read.Text, wrote.Criteria[0].Text, critText)
	}
	if wrote.Criteria[0].Status != goals.StatusMet {
		t.Errorf("dash wrote c1 status = %q, want met", wrote.Criteria[0].Status)
	}
	if read.Status != goals.StatusMet {
		t.Errorf("violet read c1 status = %q, want met (proved) — identical to the dash write", read.Status)
	}
}
