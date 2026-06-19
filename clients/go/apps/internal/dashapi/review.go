package dashapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/love-lena/sextant/protocol/sx"
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

// goalsSubject is the observable stream of goal transitions — msg.topic.goals,
// where every goal.update is published (goal/goal.update lexicons, ADR-0035).
var goalsSubject = sx.TopicSubject("goals")

// relate is one entry of an artifact record's `relates` array — the artifact-side
// handle that ties a doc to a goal criterion. Only kind=="proof" closes the loop
// (a kind=="related" is a soft cross-reference); a proof needs both goal and crit
// to flip a specific criterion.
type relate struct {
	Goal string `json:"goal"`
	Crit string `json:"crit"`
	Kind string `json:"kind"`
}

// advancedCrit reports one (goal, crit) the closed loop advanced to met — the
// optional `advanced` field on the approve response (informative; the UI already
// live-updates over SSE + the artifact poll).
type advancedCrit struct {
	Goal string `json:"goal"`
	Crit string `json:"crit"`
}

// goalUpdate is the goal.update message the closed loop emits on msg.topic.goals
// announcing a criterion transition (goal.update lexicon). $type names the record
// shape for readers; the remaining fields mirror the lexicon.
type goalUpdate struct {
	Type     string `json:"$type"`
	Goal     string `json:"goal"`
	Crit     string `json:"crit"`
	Status   string `json:"status"`
	Headline string `json:"headline"`
	Ref      string `json:"ref"`
	Updated  string `json:"updated"`
	By       string `json:"by"`
}

// closeLoop is the dash's approve→met convenience (goals-design D3): for an
// approved artifact whose record declares proof relations, it flips each
// referenced goal criterion to met and emits a goal.update. It is a dash-CLIENT
// path over the bus primitives (the goal.<id> artifact + msg.topic.goals stream),
// not a core/bus change — and exactly ONE such path, not the only way a criterion
// reaches met (an agent self-serving a mechanically-testable criterion is still
// fine).
//
// It is best-effort: the verdict write has already succeeded, so every error here
// (record without relates, goal.<id> absent, criteria parse fail, CAS conflict
// after one retry, publish error) is swallowed — a closed-loop hiccup must never
// turn the approve into an error. It returns the (goal, crit) pairs it advanced,
// for the informative `advanced` response field.
func (s *Server) closeLoop(ctx context.Context, ref string, record json.RawMessage) []advancedCrit {
	var advanced []advancedCrit
	seen := map[string]bool{} // dedup proof relations by (goal, crit)
	for _, rel := range proofRelations(record) {
		key := rel.Goal + "\x00" + rel.Crit
		if seen[key] {
			continue
		}
		seen[key] = true
		if s.flipCriterion(ctx, rel.Goal, rel.Crit, ref) {
			advanced = append(advanced, advancedCrit{Goal: rel.Goal, Crit: rel.Crit})
		}
	}
	return advanced
}

// proofRelations parses record.relates and returns the proof relations that name
// both a goal and a crit — the ones the closed loop can act on. A non-object
// record, an absent/non-array relates, or any parse failure yields nothing (the
// loop simply does no work). Non-proof kinds and crit-less proofs are filtered
// here so closeLoop's body stays about the flip.
func proofRelations(record json.RawMessage) []relate {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(record, &obj); err != nil {
		return nil
	}
	raw, ok := obj["relates"]
	if !ok {
		return nil
	}
	var all []relate
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	var proofs []relate
	for _, rel := range all {
		if rel.Kind == "proof" && rel.Goal != "" && rel.Crit != "" {
			proofs = append(proofs, rel)
		}
	}
	return proofs
}

// flipCriterion sets criterion crit of goal goalID to "met" in the goal.<id>
// artifact (CAS-write) and emits a goal.update on success. It is idempotent (an
// already-met criterion is a no-op: no write, no emit) and best-effort (any
// failure returns false and is swallowed by the caller). On a CAS conflict it
// re-gets the goal and reapplies ONCE, then gives up. It returns true only when a
// transition actually happened and was announced.
func (s *Server) flipCriterion(ctx context.Context, goalID, crit, ref string) bool {
	const attempts = 2
	for i := 0; i < attempts; i++ {
		art, err := s.bus.GetArtifact(ctx, "goal."+goalID)
		if err != nil {
			return false
		}
		merged, changed, err := setCriterionMet([]byte(art.Record), crit)
		if err != nil {
			return false
		}
		if !changed {
			return false // criterion absent or already met — idempotent no-op
		}
		if _, err := s.bus.UpdateArtifact(ctx, "goal."+goalID, merged, art.Revision); err != nil {
			if i == attempts-1 {
				return false // exhausted the one retry — give up (best-effort)
			}
			continue // a concurrent write moved the revision — re-get and reapply
		}
		s.emitGoalUpdate(ctx, goalID, crit, ref)
		return true
	}
	return false
}

// setCriterionMet rewrites a goal record with criterion crit set to status "met",
// preserving every other field (the criterion's own text/owner, sibling criteria,
// northstar, etc.). It reports changed=false — and returns the record untouched —
// when the criterion is absent or already met, so the caller can skip the write
// (idempotent). A record that isn't the expected goal shape is an error.
func setCriterionMet(record []byte, crit string) (json.RawMessage, bool, error) {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(record, &obj); err != nil {
		return nil, false, err
	}
	var criteria []map[string]json.RawMessage
	if raw, ok := obj["criteria"]; ok {
		if err := json.Unmarshal(raw, &criteria); err != nil {
			return nil, false, err
		}
	}
	changed := false
	for _, c := range criteria {
		var id, status string
		_ = json.Unmarshal(c["id"], &id)
		_ = json.Unmarshal(c["status"], &status)
		if id != crit {
			continue
		}
		if status == "met" {
			return nil, false, nil // already met — nothing to do
		}
		met, err := json.Marshal("met")
		if err != nil {
			return nil, false, err
		}
		c["status"] = met
		changed = true
		break
	}
	if !changed {
		return nil, false, nil
	}
	rebuilt, err := json.Marshal(criteria)
	if err != nil {
		return nil, false, err
	}
	obj["criteria"] = rebuilt
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// emitGoalUpdate publishes a goal.update on msg.topic.goals announcing that crit
// moved to met because ref was approved. A publish error is swallowed — the goal
// write already landed, so the transition stands even if the announcement
// doesn't.
func (s *Server) emitGoalUpdate(ctx context.Context, goalID, crit, ref string) {
	rec, err := json.Marshal(goalUpdate{
		Type:     "goal.update",
		Goal:     goalID,
		Crit:     crit,
		Status:   "met",
		Headline: "Criterion met — " + ref + " approved",
		Ref:      ref,
		Updated:  time.Now().UTC().Format(time.RFC3339),
		By:       s.bus.ID(),
	})
	if err != nil {
		return
	}
	_ = s.bus.Publish(ctx, goalsSubject, rec)
}

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
