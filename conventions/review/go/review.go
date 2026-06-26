// Package review is the review convention in Go (ADR-0044, TASK-239): the
// co-equal peer of conventions/review/ts. An artifact carries a review-state as a
// `review` block inside its record — NOT a change to the core artifact primitive
// (create/get/update/list are untouched). Absent => the UI reads the artifact as
// neutral (draft); a producer sets state="review" explicitly when the artifact is
// for the operator's judgment. This is the logic the dash's review surface used to
// run server-side (the old dashapi/review.go); it moved to a convention so the
// browser dash runs it directly over its own bus Client, and this Go peer lets a
// headless Go agent run the SAME read-merge-CAS + approve→met closed loop.
//
// As an engine-as-a-library (ADR-0011), a verb here translates a domain action
// (the operator's verdict) into the same primitive bus operations a bare client
// could issue — get, compare-and-set, publish — reaching the bus only through the
// Ops seam (the structural subset of the SDK's Client it needs). importcheck holds
// this package's production closure to the SDK, the protocol bindings, and the
// sibling goals convention (for the closed loop); it forbids the bus.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	goals "github.com/love-lena/sextant/conventions/goal/go"
)

// Ops is the primitive bus surface the review verbs are written against — get,
// compare-and-set, publish — declared identical to goals.Ops so the same SDK
// *Client (or a recorder/fake) satisfies both, and so closeLoop can hand its ops
// straight to goals.SetCriterion. It is a consumer-defined interface (declared
// where it is used, kept small), re-declared here rather than imported from goals
// so the review convention does not couple to the goals seam at the type level.
type Ops interface {
	// GetArtifact reads an artifact's current record and revision.
	GetArtifact(ctx context.Context, name string) (record json.RawMessage, revision uint64, err error)
	// UpdateArtifact compare-and-sets an artifact's record; expectedRev guards a
	// lost update. Returns the new revision.
	UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error)
	// Publish issues a message.publish on subject (must be under msg.) with record.
	Publish(ctx context.Context, subject string, record json.RawMessage) error
}

// ReviewStates are the states the convention recognises (the peer of TS's
// REVIEW_STATES). A verdict outside this set is rejected so a typo never persists.
var ReviewStates = []string{"review", "approved", "changes", "draft", "rejected", "archived"}

// IsReviewState reports whether s is a recognised review state.
func IsReviewState(s string) bool {
	for _, st := range ReviewStates {
		if s == st {
			return true
		}
	}
	return false
}

// ReviewBlock is the convention's record field (the peer of TS's ReviewBlock). It
// has two halves: State is the producer's needs-your-eyes INTENT (settable by
// anyone, no verdict fields), and By/At/Rev are the operator's VERDICT, set on
// approve/changes. The verdict fields are omitted when empty so a producer-set,
// state-only block round-trips without phantom attribution — matching the TS peer
// byte-for-byte under the canonical rule.
type ReviewBlock struct {
	State string `json:"state"`
	By    string `json:"by,omitempty"`
	At    string `json:"at,omitempty"`
	Rev   uint64 `json:"rev,omitempty"` // the artifact revision this review was made against
}

// SetReviewInput is the domain input to SetReview: the artifact name, the new
// state, who is making the verdict (the bus-stamped author of the write is
// authoritative; By is the convenience label), and Now — the verdict timestamp
// (RFC3339). The live caller passes the real time; the recorded conformance verb
// passes a fixed time so the merged record is byte-stable. The field names mirror
// the TS SetReviewInput exactly.
type SetReviewInput struct {
	Name  string `json:"name"`
	State string `json:"state"`
	By    string `json:"by"`
	Now   string `json:"now"`
}

// AdvancedCrit reports one (goal, crit) the closed loop advanced to met — the peer
// of TS's AdvancedCrit. Returned by SetReview on an approve so the UI can surface
// "this approval moved goal X criterion Y to met".
type AdvancedCrit struct {
	Goal string `json:"goal"`
	Crit string `json:"crit"`
}

// SetReviewResult is SetReview's outcome: the artifact name, its new revision, the
// persisted state, and the criteria the approve closed-loop advanced (empty unless
// State=="approved").
type SetReviewResult struct {
	Name     string
	Revision uint64
	Review   string
	Advanced []AdvancedCrit
}

// ErrReview wraps a failed SetReview: an invalid state, a get failure (the
// artifact was not found), or an update failure after the retry (the verdict did
// not persist) or a malformed record. The closed loop is best-effort and never
// surfaces an error — its failures are swallowed, the verdict is the primary
// outcome. Match with errors.Is.
var ErrReview = errors.New("review")

// SetReview persists an artifact's review-state by merging a `review` block into
// its record (read → merge → compare-and-set), preserving every other top-level
// field. A stale CAS is retried ONCE before reporting a conflict (the peer of the
// TS verb's attempts=2 loop). On an approve it then runs the closed loop
// (best-effort): flip any proof-related goal criteria to met and announce them via
// the goals convention's single write path. The verdict write is the primary
// outcome — a closed-loop hiccup never fails it.
func SetReview(ctx context.Context, ops Ops, in SetReviewInput) (SetReviewResult, error) {
	if !IsReviewState(in.State) {
		return SetReviewResult{}, fmt.Errorf("%w: state must be one of the recognised review states", ErrReview)
	}
	const attempts = 2
	for i := 0; i < attempts; i++ {
		record, rev, err := ops.GetArtifact(ctx, in.Name)
		if err != nil {
			return SetReviewResult{}, fmt.Errorf("%w: get artifact %q: %w", ErrReview, in.Name, err)
		}
		merged, err := MergeReview(record, ReviewBlock{State: in.State, By: in.By, At: in.Now, Rev: rev})
		if err != nil {
			return SetReviewResult{}, fmt.Errorf("%w: %w", ErrReview, err)
		}
		newRev, err := ops.UpdateArtifact(ctx, in.Name, merged, rev)
		if err != nil {
			if i == attempts-1 {
				return SetReviewResult{}, fmt.Errorf("%w: update failed: %w", ErrReview, err)
			}
			continue // a concurrent write moved the revision — re-get and reapply
		}
		// The verdict is persisted — the primary outcome. On an approve, run the
		// closed loop (goals-design D3) as a best-effort convenience over the
		// pre-merge record (which carries the proof relations).
		var advanced []AdvancedCrit
		if in.State == "approved" {
			advanced = closeLoop(ctx, ops, in.Name, record, in.By, in.Now)
		}
		return SetReviewResult{Name: in.Name, Revision: newRev, Review: in.State, Advanced: advanced}, nil
	}
	// Unreachable (the loop returns or errors), but Go needs a terminus.
	return SetReviewResult{}, fmt.Errorf("%w: update failed", ErrReview)
}

// closeLoop is the approve→met convenience (goals-design D3, the peer of TS's
// closeLoop): for an approved artifact whose record declares proof relations, it
// flips each referenced goal criterion to met and announces it via the goals
// convention's single write path (goals.SetCriterion). The review convention holds
// no goal mechanics of its own; what counts as a proof relation is
// goals.ProofRelations, the one definition both halves share.
//
// It is best-effort: the verdict write has already succeeded, so every error here
// (record without relates, goal.<id> absent, a CAS conflict, a publish error) is
// swallowed — a closed-loop hiccup must never turn the approve into an error. It
// retries each criterion ONCE on a conflict. It returns the (goal, crit) pairs it
// advanced.
func closeLoop(ctx context.Context, ops Ops, ref string, record json.RawMessage, by, now string) []AdvancedCrit {
	var advanced []AdvancedCrit
	seen := map[string]bool{} // dedup proof relations by (goal, crit)
	for _, rel := range goals.ProofRelations(record) {
		key := rel.Goal + "\x00" + rel.Crit
		if seen[key] {
			continue
		}
		seen[key] = true
		if flipToMet(ctx, ops, rel.Goal, rel.Crit, ref, by, now) {
			advanced = append(advanced, AdvancedCrit{Goal: rel.Goal, Crit: rel.Crit})
		}
	}
	return advanced
}

// flipToMet sets one goal criterion to met via the goals convention. It returns
// true when the criterion actually moved to met, false when nothing moved (an
// already-met or absent criterion is an idempotent no-op). The caller is
// best-effort, so failures are not propagated; the retry is precise (the peer of
// TS's flipToMet):
//   - goals.ErrUpdate (a CAS lost a race) is the only retryable failure — re-run
//     SetCriterion ONCE, then give up. A get/rewrite failure is not retried.
//   - goals.ErrPublish means the goal write LANDED but the announce didn't. The
//     criterion moved, so this counts as advanced (true); we do not retry.
func flipToMet(ctx context.Context, ops Ops, goalID, crit, ref, by, now string) bool {
	in := goals.SetCriterionInput{
		GoalID:      goalID,
		CriterionID: crit,
		Status:      goals.StatusMet,
		Headline:    "Criterion met — " + ref + " approved",
		Ref:         ref,
		By:          by,
	}
	const attempts = 2
	for i := 0; i < attempts; i++ {
		// ops (review.Ops) has the same method set as goals.Ops, so it satisfies it
		// directly — no adapter.
		changed, err := goals.SetCriterion(ctx, ops, in, now)
		if err == nil {
			return changed
		}
		if errors.Is(err, goals.ErrPublish) {
			return true // the write landed; only the announce missed — it advanced
		}
		if errors.Is(err, goals.ErrUpdate) && i < attempts-1 {
			continue // a concurrent write moved the revision — re-get and reapply once
		}
		return false // a get/rewrite failure, or the retry is exhausted — give up
	}
	return false
}

// MergeReview rewrites record with the review block set, preserving every other
// top-level field (the peer of TS's mergeReview). It rewrites at the
// json.RawMessage level rather than round-tripping through a typed struct so an
// unknown field a future lexicon adds is preserved rather than dropped — the write
// path must never silently lose content the bus owns. A nil/empty record yields a
// record carrying only the review block (a document always has fields, but a
// defensive empty is valid JSON); a non-object record is an error so the merge
// never silently drops content.
func MergeReview(record json.RawMessage, rb ReviewBlock) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(record) > 0 {
		if err := json.Unmarshal(record, &obj); err != nil {
			return nil, fmt.Errorf("record is not a JSON object: %w", err)
		}
	}
	block, err := json.Marshal(rb)
	if err != nil {
		return nil, fmt.Errorf("marshal review block: %w", err)
	}
	obj["review"] = block
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal merged record: %w", err)
	}
	return out, nil
}

// Read parses the review block out of an artifact record (the peer of TS's read):
// it returns the block and ok=true when the record carries one, or ok=false for a
// record with no review block (which the UI reads as neutral/draft). A malformed
// record or review block yields ok=false rather than an error — reading a state is
// best-effort, the absence of a verdict is itself meaningful.
func Read(record json.RawMessage) (ReviewBlock, bool) {
	obj := map[string]json.RawMessage{}
	if json.Unmarshal(record, &obj) != nil {
		return ReviewBlock{}, false
	}
	raw, ok := obj["review"]
	if !ok {
		return ReviewBlock{}, false
	}
	var rb ReviewBlock
	if json.Unmarshal(raw, &rb) != nil {
		return ReviewBlock{}, false
	}
	return rb, true
}
