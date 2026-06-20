package dashapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/love-lena/sextant/clients/go/conventions/goals"
)

// The review convention (TASK-66, brief workstream): an artifact carries a
// review-state as a `review` block inside its record — NOT a change to the core
// artifact primitive (create/get/update/list are untouched). Absent ⇒ the dash
// reads the artifact as neutral (draft); a producer sets state="review"
// explicitly when the artifact is for the operator's judgment (TASK-112). The
// companion discussion topic is msg.topic.artifact.<name>, posted to over
// /api/publish by the UI; this endpoint only persists the operator's verdict.

// reviewStates are the states the convention recognises.
var reviewStates = map[string]bool{
	"review": true, "approved": true, "changes": true, "draft": true,
	"rejected": true, "archived": true,
}

type reviewRequest struct {
	State string `json:"state"`
}

// reviewBlock is the convention's record field. It has two halves: `state` is
// the producer's needs-your-eyes INTENT (settable by anyone, no verdict fields),
// and `by`/`at`/`rev` are the operator's VERDICT, server-set by this endpoint on
// approve/changes. The verdict fields are omitempty so a producer-set,
// state-only block (TASK-112: absent ⇒ neutral; review-state set explicitly)
// round-trips without phantom attribution. A real verdict always stamps a
// non-zero rev + identity, so they serialize as before.
type reviewBlock struct {
	State string `json:"state"`
	By    string `json:"by,omitempty"`
	At    string `json:"at,omitempty"`
	Rev   uint64 `json:"rev,omitempty"` // the artifact revision this review was made against
}

// handleArtifactReview persists an artifact's review-state by merging a `review`
// block into its record (read → merge → compare-and-set). The original record
// fields are preserved; a stale CAS is retried once before reporting a conflict.
// A failed get is reported 404 (mirroring handleArtifactGet's coarse taxonomy).
func (s *Server) handleArtifactReview(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req reviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON {state}")
		return
	}
	if !reviewStates[req.State] {
		writeError(w, http.StatusBadRequest, "state must be one of: review, approved, changes, draft, rejected, archived")
		return
	}

	const attempts = 2
	for i := 0; i < attempts; i++ {
		art, err := s.bus.GetArtifact(r.Context(), name)
		if err != nil {
			writeError(w, http.StatusNotFound, "artifact not found")
			return
		}
		merged, err := mergeReview(art.Record, reviewBlock{
			State: req.State, By: s.bus.ID(), At: time.Now().UTC().Format(time.RFC3339), Rev: art.Revision,
		})
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "artifact record is not a JSON object")
			return
		}
		rev, err := s.bus.UpdateArtifact(r.Context(), name, merged, art.Revision)
		if err == nil {
			// The verdict is now persisted — the primary outcome. On an approve,
			// run the closed loop (goals-design D3) as a best-effort convenience:
			// flip any proof-related goal criteria to met and announce them. Its
			// result is informative only; a failure never demotes the 200.
			var advanced []advancedCrit
			if req.State == "approved" {
				advanced = s.closeLoop(r.Context(), name, art.Record)
			}
			resp := map[string]any{"name": name, "revision": rev, "review": req.State}
			if len(advanced) > 0 {
				resp["advanced"] = advanced
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if i == attempts-1 {
			writeError(w, http.StatusBadGateway, "update failed: "+err.Error())
			return
		}
		// else: a concurrent write moved the revision — re-get and reapply.
	}
}

// advancedCrit reports one (goal, crit) the closed loop advanced to met — the
// optional `advanced` field on the approve response (informative; the UI already
// live-updates over SSE + the artifact poll).
type advancedCrit struct {
	Goal string `json:"goal"`
	Crit string `json:"crit"`
}

// closeLoop is the dash's approve→met convenience (goals-design D3): for an
// approved artifact whose record declares proof relations, it flips each
// referenced goal criterion to met and announces it. The flip itself is the
// goals convention's single write path — goals.SetCriterion (CAS the goal
// artifact + emit goal.update on msg.topic.goals) — so the dash holds no goal
// mechanics of its own; what counts as a proof relation is goals.ProofRelations,
// the one definition both halves share. It is exactly ONE path to met, not the
// only one (an agent self-serving a mechanically-testable criterion is still
// fine).
//
// It is best-effort: the verdict write has already succeeded, so every error here
// (record without relates, goal.<id> absent, a CAS conflict, a publish error) is
// swallowed — a closed-loop hiccup must never turn the approve into an error. It
// retries each criterion once on a conflict (SetCriterion does not loop). It
// returns the (goal, crit) pairs it advanced, for the informative `advanced`
// response field.
func (s *Server) closeLoop(ctx context.Context, ref string, record json.RawMessage) []advancedCrit {
	ops := goalsOps{bus: s.bus}
	var advanced []advancedCrit
	seen := map[string]bool{} // dedup proof relations by (goal, crit)
	for _, rel := range goals.ProofRelations(record) {
		key := rel.Goal + "\x00" + rel.Crit
		if seen[key] {
			continue
		}
		seen[key] = true
		if flipToMet(ctx, ops, rel.Goal, rel.Crit, ref, s.bus.ID()) {
			advanced = append(advanced, advancedCrit{Goal: rel.Goal, Crit: rel.Crit})
		}
	}
	return advanced
}

// flipToMet sets one goal criterion to met via the goals convention, retrying
// once on a CAS conflict (the verb is single-shot; the dash owns the retry it
// needs). It returns true only when a transition actually happened and was
// announced — an already-met or absent criterion is an idempotent no-op (false).
// Every error is swallowed: the caller is best-effort.
func flipToMet(ctx context.Context, ops goalsOps, goalID, crit, ref, by string) bool {
	const attempts = 2
	for i := 0; i < attempts; i++ {
		changed, err := goals.SetCriterion(ctx, ops, goals.SetCriterionInput{
			GoalID:      goalID,
			CriterionID: crit,
			Status:      goals.StatusMet,
			Headline:    "Criterion met — " + ref + " approved",
			Ref:         ref,
			By:          by,
		}, time.Now().UTC().Format(time.RFC3339))
		if err == nil {
			return changed
		}
		// err is a get/update/publish failure; the only one worth a retry is a CAS
		// conflict on the update. Re-get and reapply once, then give up.
	}
	return false
}

// goalsOps adapts the dash's Bus to goals.Ops (the convention verb's primitive
// surface): a one-method-each pass-through that projects the SDK's richer
// GetArtifact return down to the (record, revision) a verb needs. The dash never
// reimplements goal mechanics — it drives the convention through this seam.
type goalsOps struct{ bus Bus }

func (o goalsOps) GetArtifact(ctx context.Context, name string) (json.RawMessage, uint64, error) {
	art, err := o.bus.GetArtifact(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	return json.RawMessage(art.Record), art.Revision, nil
}

func (o goalsOps) UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	return o.bus.UpdateArtifact(ctx, name, record, expectedRev)
}

func (o goalsOps) Publish(ctx context.Context, subject string, record json.RawMessage) error {
	return o.bus.Publish(ctx, subject, record)
}

var _ goals.Ops = goalsOps{}

// mergeReview rewrites record with the review block set, preserving every other
// top-level field. record must be a JSON object (documents are); a non-object
// record is rejected so the merge never silently drops content.
func mergeReview(record json.RawMessage, rb reviewBlock) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(record) > 0 {
		if err := json.Unmarshal(record, &obj); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(rb)
	if err != nil {
		return nil, err
	}
	obj["review"] = b
	return json.Marshal(obj)
}
