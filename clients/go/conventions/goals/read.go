package goals

import (
	"encoding/json"
)

// The read side of the goals convention: parse a goal record, derive its status
// from the criteria rollup, and apply the proof-filter — the met-needs-proof
// invariant. Both the dash backend and violet read goals through here, so the
// reading rule lives in one place and cannot drift.

// Relate is one entry of an artifact record's `relates` array — the handle that
// ties a document to a goal criterion. A kind=="proof" relation naming both a
// goal and a crit is what backs a criterion's invariant "met"; a kind=="related"
// is a soft cross-reference that does not.
type Relate struct {
	Goal string `json:"goal"`
	Crit string `json:"crit"`
	Kind string `json:"kind"`
}

// ParseRelates reads the `relates` array from any artifact record. A missing or
// malformed relates field yields nil (most artifacts carry no relations) — never
// an error. It is the single parser both the write path (which proof relations
// close a loop) and the read path (which criteria have proof) share.
func ParseRelates(record json.RawMessage) []Relate {
	obj := map[string]json.RawMessage{}
	if json.Unmarshal(record, &obj) != nil {
		return nil
	}
	raw, ok := obj["relates"]
	if !ok {
		return nil
	}
	var all []Relate
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	return all
}

// ProofRelations returns the proof relations among a record's relates that name
// both a goal and a crit — the ones that can back a met criterion (and the ones
// the dash's approve→met loop acts on). Non-proof kinds and crit-less proofs are
// filtered out. This is the SINGLE definition of "what counts as proof",
// replacing the two that had diverged (the dash write side filtered Goal!="" &&
// Crit!=""; violet's read side filtered only on Kind=="proof").
func ProofRelations(record json.RawMessage) []Relate {
	var proofs []Relate
	for _, r := range ParseRelates(record) {
		if r.Kind == "proof" && r.Goal != "" && r.Crit != "" {
			proofs = append(proofs, r)
		}
	}
	return proofs
}

// ParseGoal decodes a goal record into the generated Goal type. ok is false when
// the record is not a goal — it has no criteria array (the same recognition the
// read side has always used: a goal is the artifact that carries criteria). A
// record that has criteria but is otherwise malformed returns ok=false rather
// than a partial goal.
func ParseGoal(record json.RawMessage) (Goal, bool) {
	obj := map[string]json.RawMessage{}
	if json.Unmarshal(record, &obj) != nil {
		return Goal{}, false
	}
	if _, ok := obj["criteria"]; !ok {
		return Goal{}, false
	}
	var g Goal
	if json.Unmarshal(record, &g) != nil {
		return Goal{}, false
	}
	return g, true
}

// CriterionMet reports whether criterion c reads as met, applying the proof
// invariant: a criterion is met only when its stored status is "met" AND at least
// one proof-kind artifact backs it (provedCrits[c.ID] is true). A stored "met"
// with no proof reads as in-progress — the work claims done, but nothing yet
// substantiates it. provedCrits is the set of criterion ids the caller found a
// proof artifact for (see [ProvedCriteria]); pass nil to treat no criterion as
// proved (every stored "met" then downgrades).
//
// This is the proof-filter, in one place. It is the read-side arbiter of the
// lexicon's "met (satisfied; invariant — has >=1 proof-kind artifact in relates)":
// the write path may set "met", but the read shows met only with proof, so the
// dash and violet can never disagree about whether a goal is done.
func CriterionMet(c Criterion, provedCrits map[string]bool) bool {
	return c.Status == StatusMet && provedCrits[c.ID]
}

// EffectiveStatus returns a criterion's status as it should READ, applying the
// proof-filter: a stored "met" without proof reads as "in-progress"; every other
// status reads as stored. It is what a UI or digest should display.
func EffectiveStatus(c Criterion, provedCrits map[string]bool) string {
	if c.Status == StatusMet && !provedCrits[c.ID] {
		return StatusInProgress
	}
	return c.Status
}

// ProvedCriteria builds the proved-criteria set for goal goalID from a set of
// artifact records (the artifacts directory). A criterion id is in the set when
// some artifact's relates carries a proof relation naming this goal and that
// criterion. The caller passes the records it has already read (the read side
// lists once, then gets each artifact); this turns them into the proof lookup
// [CriterionMet] / [EffectiveStatus] consult.
func ProvedCriteria(goalID string, records []json.RawMessage) map[string]bool {
	proved := map[string]bool{}
	for _, rec := range records {
		for _, p := range ProofRelations(rec) {
			if p.Goal == goalID {
				proved[p.Crit] = true
			}
		}
	}
	return proved
}

// Rollup is the derived view of a goal's progress: how many criteria are met
// (after the proof-filter) of the total, and the salient flags a front-end groups
// on. Goal status is DERIVED — there is no stored goal-status field.
type Rollup struct {
	Met     int  // criteria that read as met (status met AND proved)
	Total   int  // total criteria
	Waiting int  // criteria waiting on the operator
	Blocked bool // any criterion hard-blocked
	Defined bool // has a north-star and at least one criterion
}

// Rollup derives goal g's progress, applying the proof-filter via provedCrits.
// A criterion's "met" only counts when proved; an unproved stored "met" counts as
// in-progress (neither met nor waiting nor blocked), which is exactly how it reads
// elsewhere. Defined is false for a goal with no north-star or no criteria.
func (g Goal) Rollup(provedCrits map[string]bool) Rollup {
	r := Rollup{Total: len(g.Criteria), Defined: g.Northstar != "" && len(g.Criteria) > 0}
	for _, c := range g.Criteria {
		switch EffectiveStatus(c, provedCrits) {
		case StatusMet:
			r.Met++
		case StatusWaitingOnYou:
			r.Waiting++
		case StatusBlocked:
			r.Blocked = true
		}
	}
	return r
}
