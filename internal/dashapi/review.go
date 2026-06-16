package dashapi

import (
	"encoding/json"
	"net/http"
	"time"
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
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "revision": rev, "review": req.State})
			return
		}
		if i == attempts-1 {
			writeError(w, http.StatusBadGateway, "update failed: "+err.Error())
			return
		}
		// else: a concurrent write moved the revision — re-get and reapply.
	}
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
