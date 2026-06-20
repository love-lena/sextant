// Package goals is the goals convention: the single home for goal mechanics
// (ADR-0041, TASK-173). A goal is a shared objective — a north-star sentence plus
// the acceptance criteria that define "done" — published as the latest-value
// artifact goal.<id>, with transitions announced on msg.topic.goals as goal.update
// messages (ADR-0035). The goal's STATUS is derived from its criteria rollup, never
// stored.
//
// This package replaces two hand-rolled halves that had silently drifted: the dash
// WRITE half (set a criterion, announce it) and the violet READ half (digest a
// goal for the operator's home). They disagreed on field names — violet read
// criteria off `label`/`state` while the lexicon and the dash write `text`/`status`,
// and violet read the headline off `title` rather than `northstar`. The fix is one
// set of generated record types (goal_gen.go, from the lexicon) that both halves
// consume, so the drift is structurally impossible: there is one Goal type, with
// one set of field names, and it comes from the contract.
//
// As an engine-as-a-library (ADR-0011), a verb here translates a domain action into
// the same primitive bus operations a bare client could issue — get, compare-and-set,
// publish. It reaches the bus only through the SDK (the Ops seam below); importcheck
// holds this package's production closure to the SDK and the protocol bindings and
// forbids the bus.
package goals

//go:generate go run ./internal/lexgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/love-lena/sextant/protocol/sx"
)

// GoalsSubject is the observable stream of goal transitions: msg.topic.goals,
// where every goal.update is published (ADR-0035). The artifact goal.<id> carries
// the current value; this topic carries the events.
var GoalsSubject = sx.TopicSubject("goals")

// ArtifactName is the artifact a goal's current value lives in: goal.<id>.
func ArtifactName(goalID string) string { return "goal." + goalID }

// Statuses a criterion may hold (the goal lexicon's enum). met is invariant —
// it reads as met only with proof, see [CriterionMet]; the rest are signalled by
// whoever is doing the work.
const (
	StatusMet          = "met"
	StatusInProgress   = "in-progress"
	StatusWaitingOnYou = "waiting-on-you"
	StatusBlocked      = "blocked"
	StatusNotStarted   = "not-started"
)

// Ops is the primitive bus surface the goal verbs are written against — the
// subset of the SDK a verb needs: read an artifact, compare-and-set it, publish
// a message. It is a consumer-defined interface (declared where it is used, kept
// small), so the same verb runs live against the SDK *Client and is recorded
// against the conformance Recorder, neither importing the other. The SDK's
// *Client satisfies it directly; SDKOps in the conformance package adapts a
// *Client to the same shape for the recorded/e2e path.
type Ops interface {
	// GetArtifact reads an artifact's current record and revision.
	GetArtifact(ctx context.Context, name string) (record json.RawMessage, revision uint64, err error)
	// UpdateArtifact compare-and-sets an artifact's record; expectedRev guards a
	// lost update. Returns the new revision.
	UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error)
	// Publish issues a message.publish on subject (must be under msg.) with record.
	Publish(ctx context.Context, subject string, record json.RawMessage) error
}

// SetCriterionInput is the domain input to [SetCriterion] — the verb's signature,
// mirrored in the goal lexicon's verbs.setCriterion (the contract TS implements).
type SetCriterionInput struct {
	// GoalID identifies the goal; the artifact is goal.<GoalID>.
	GoalID string `json:"goalId"`
	// CriterionID is the criterion to set, unique within the goal.
	CriterionID string `json:"criterionId"`
	// Status is the new status — one of the Status* constants.
	Status string `json:"status"`
	// Headline is a short line describing the transition, for the announcement.
	Headline string `json:"headline"`
	// Ref is an optional artifact/PR/ticket that triggered the update.
	Ref string `json:"ref,omitempty"`
	// By is an optional convenience label for who set it (the bus-stamped author
	// of the write is authoritative).
	By string `json:"by,omitempty"`
}

// Update is the goal.update message a transition announces on [GoalsSubject]
// (protocol/lexicons/goal.update.json): an observation that a goal moved, not its
// current value. It signals; it does not manage.
type Update struct {
	Type     string `json:"$type"`
	Goal     string `json:"goal"`
	Crit     string `json:"crit,omitempty"`
	Status   string `json:"status,omitempty"`
	Headline string `json:"headline"`
	Ref      string `json:"ref,omitempty"`
	Updated  string `json:"updated,omitempty"`
	By       string `json:"by,omitempty"`
}

// Error sentinels mark WHICH step of SetCriterion failed, so a caller can react
// precisely — notably the dash's best-effort approve loop, which retries ONLY a
// failed compare-and-set update (a concurrent write moved the revision) and never
// a get or a publish. Match with errors.Is.
var (
	// ErrGet wraps a failure reading the goal artifact before the write.
	ErrGet = errors.New("goals: get goal")
	// ErrUpdate wraps a failed compare-and-set of the goal artifact — the failure
	// a read-modify-write retries (re-get, reapply). A persistent ErrUpdate is a
	// real write failure, not a transient conflict.
	ErrUpdate = errors.New("goals: update goal")
	// ErrPublish wraps a failure announcing the transition on msg.topic.goals
	// AFTER the goal write already landed. The criterion HAS moved; only the
	// announcement failed, so a caller must NOT retry the write (it would no-op)
	// — it may re-announce or surface the miss, never re-run the verb.
	ErrPublish = errors.New("goals: publish goal.update")
)

// SetCriterion sets one criterion's status on a goal: it reads goal.<GoalID>,
// rewrites that criterion's status in place (every other field preserved),
// compare-and-sets it back, then announces the transition on msg.topic.goals.
// This is the convention's single write path, generalising the dash's old
// approve→met flip to any status.
//
// It is idempotent: a criterion already at the target status is a no-op — no
// write, no announce — and changed reports false. The verb itself does not loop;
// its recorded transcript is a single get→update→publish. A failure is wrapped
// with the step sentinel ([ErrGet]/[ErrUpdate]/[ErrPublish]) so a best-effort
// caller retries only the CAS update and distinguishes a write that landed but
// failed to announce.
//
// The criterion must exist; setting an absent criterion is a no-op with
// changed=false (not an error — the caller may be racing a goal edit). A record
// that is not a goal shape is an error.
func SetCriterion(ctx context.Context, ops Ops, in SetCriterionInput, now string) (changed bool, err error) {
	name := ArtifactName(in.GoalID)
	record, rev, err := ops.GetArtifact(ctx, name)
	if err != nil {
		return false, fmt.Errorf("%w %s: %w", ErrGet, name, err)
	}
	merged, changed, err := setCriterionStatus(record, in.CriterionID, in.Status)
	if err != nil {
		return false, fmt.Errorf("goals: rewrite criterion %q on %s: %w", in.CriterionID, name, err)
	}
	if !changed {
		return false, nil
	}
	if _, err := ops.UpdateArtifact(ctx, name, merged, rev); err != nil {
		return false, fmt.Errorf("%w %s: %w", ErrUpdate, name, err)
	}
	update, err := json.Marshal(Update{
		Type:     "goal.update",
		Goal:     in.GoalID,
		Crit:     in.CriterionID,
		Status:   in.Status,
		Headline: in.Headline,
		Ref:      in.Ref,
		Updated:  now,
		By:       in.By,
	})
	if err != nil {
		return false, fmt.Errorf("goals: marshal goal.update: %w", err)
	}
	if err := ops.Publish(ctx, GoalsSubject, update); err != nil {
		// The write landed; only the announcement failed. Report changed=true (the
		// criterion DID move) alongside ErrPublish so the caller knows the
		// transition stands and must not retry the write.
		return true, fmt.Errorf("%w: %w", ErrPublish, err)
	}
	return true, nil
}

// setCriterionStatus rewrites a goal record with criterion crit set to status,
// preserving every other field (the criterion's own text/owner, sibling criteria,
// the north-star). It reports changed=false — and returns the record untouched —
// when the criterion is absent or already at status, so the caller can skip the
// write. A record that is not the expected goal shape is an error.
//
// It rewrites at the json.RawMessage level rather than round-tripping through the
// generated Goal type so an unknown field a future lexicon adds is preserved
// rather than dropped — the write path must never silently lose content the bus
// owns.
func setCriterionStatus(record json.RawMessage, crit, status string) (json.RawMessage, bool, error) {
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
		var id, cur string
		_ = json.Unmarshal(c["id"], &id)
		_ = json.Unmarshal(c["status"], &cur)
		if id != crit {
			continue
		}
		if cur == status {
			return nil, false, nil // already at the target status — nothing to do
		}
		next, err := json.Marshal(status)
		if err != nil {
			return nil, false, err
		}
		c["status"] = next
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
