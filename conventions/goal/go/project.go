package goals

import (
	"encoding/json"
	"sort"
	"strings"
)

// The Goals PROJECTION: the read-model a UI renders. It is built here, once, so
// the proof-filter rule (a criterion reads met only with proof) and the evidence
// wiring live in ONE place — Go — rather than being reimplemented per client. The
// dash backend serves this projection pre-filtered; the dash JS is then a dumb
// renderer of effective statuses, never a second copy of the proof rule. violet's
// digest reads the same effective statuses through the same conv/goals calls.
//
// A projection is derived from the artifact directory: the goal.<id> records plus
// every other record (so proof relations pointing at a goal are found). It is pure
// — no bus, no IO — so the caller lists once and hands the records in.

// Artifact is one entry of the artifact directory the projection is built from:
// the artifact's name, its record, and the bus-stamped revision. The caller
// projects the SDK's richer ArtifactInfo/Artifact down to this.
type Artifact struct {
	Name     string
	Record   json.RawMessage
	Revision uint64
}

// Evidence is one artifact backing a criterion (or a goal): its name and whether
// it is proof (kind=="proof") or a softer related reference. A criterion reads
// met only when it has ≥1 proof Evidence — the invariant [EffectiveStatus]
// enforces.
type Evidence struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "proof" | "related"
}

// CriterionView is a criterion as a UI renders it: its identity and text, its
// EFFECTIVE status (proof-filter already applied), the owner, and the evidence
// backing it. Status is never the raw stored value when that would read met
// without proof — it is what should display.
type CriterionView struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Status   string     `json:"status"` // effective status (post proof-filter)
	Owner    string     `json:"owner,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

// GoalView is a goal as a UI renders it: identity, north-star, stream, the
// criteria with effective statuses + evidence, the derived rollup, the optional
// review-state, and bus-stamped revision. It is the served read-model that
// replaces a client re-deriving goals off the raw artifact directory.
type GoalView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"` // the artifact name, goal.<id>
	Northstar string          `json:"northstar"`
	Stream    string          `json:"stream,omitempty"`
	Updated   string          `json:"updated,omitempty"`
	By        string          `json:"by,omitempty"`
	Revision  uint64          `json:"revision"`
	Review    string          `json:"review,omitempty"` // review-state from the goal record (sign-off convention)
	Criteria  []CriterionView `json:"criteria"`
	Evidence  []Evidence      `json:"evidence,omitempty"` // goal-level relations (no crit)
	Rollup    Rollup          `json:"rollup"`
}

// evidenceIndex maps a goal id to the evidence backing it: per-criterion and
// goal-level (a relation with a goal but no crit). It is built once from the whole
// directory, the inverse of the relates array that points FROM an artifact AT a
// goal/criterion.
type evidenceIndex struct {
	crit map[string]map[string][]Evidence // goalID -> critID -> evidence
	goal map[string][]Evidence            // goalID -> goal-level evidence
}

func indexEvidence(arts []Artifact) evidenceIndex {
	idx := evidenceIndex{crit: map[string]map[string][]Evidence{}, goal: map[string][]Evidence{}}
	for _, a := range arts {
		for _, rel := range ParseRelates(a.Record) {
			if rel.Goal == "" {
				continue
			}
			kind := "related"
			if rel.Kind == "proof" {
				kind = "proof"
			}
			ev := Evidence{Name: a.Name, Kind: kind}
			if rel.Crit != "" {
				byCrit := idx.crit[rel.Goal]
				if byCrit == nil {
					byCrit = map[string][]Evidence{}
					idx.crit[rel.Goal] = byCrit
				}
				byCrit[rel.Crit] = append(byCrit[rel.Crit], ev)
			} else {
				idx.goal[rel.Goal] = append(idx.goal[rel.Goal], ev)
			}
		}
	}
	return idx
}

// provedFrom returns the proved-criteria set for goalID from the evidence index:
// a criterion is proved when it has ≥1 proof-kind evidence. This is the same
// invariant [ProvedCriteria] computes from raw records; the projection reuses the
// already-built index rather than re-scanning.
func (idx evidenceIndex) provedFrom(goalID string) map[string]bool {
	proved := map[string]bool{}
	for crit, evs := range idx.crit[goalID] {
		for _, e := range evs {
			if e.Kind == "proof" {
				proved[crit] = true
				break
			}
		}
	}
	return proved
}

// Project builds the Goals read-model from the artifact directory: one GoalView
// per goal.<id> record, each with the proof-filter applied (effective statuses),
// the derived rollup, evidence wired in, and the review-state read off the goal
// record. The views are sorted by name for stable rendering. A non-goal artifact
// is ignored (it may still be a proof source). This is THE place the proof rule
// turns the stored goal into what a UI shows.
func Project(arts []Artifact) []GoalView {
	idx := indexEvidence(arts)
	var views []GoalView
	for _, a := range arts {
		g, ok := ParseGoal(a.Record)
		if !ok {
			continue
		}
		id := strings.TrimPrefix(a.Name, "goal.")
		proved := idx.provedFrom(id)
		view := GoalView{
			ID:        id,
			Name:      a.Name,
			Northstar: g.Northstar,
			Stream:    g.Stream,
			Updated:   g.Updated,
			By:        g.By,
			Revision:  a.Revision,
			Review:    reviewState(a.Record),
			Evidence:  idx.goal[id],
			Rollup:    g.Rollup(proved),
		}
		for _, c := range g.Criteria {
			view.Criteria = append(view.Criteria, CriterionView{
				ID:       c.ID,
				Text:     c.Text,
				Status:   EffectiveStatus(c, proved),
				Owner:    c.Owner,
				Evidence: idx.crit[id][c.ID],
			})
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

// reviewState reads the review-state convention block (an artifact's `review`
// object with a `state` field; ADR review convention) off a goal record, for the
// sign-off affordance the UI shows. Absent/malformed ⇒ "" (neutral). The goals
// convention does not own review-state, but the goal projection carries it so the
// UI reads one shape.
func reviewState(record json.RawMessage) string {
	var rec struct {
		Review struct {
			State string `json:"state"`
		} `json:"review"`
	}
	if json.Unmarshal(record, &rec) != nil {
		return ""
	}
	return rec.Review.State
}
