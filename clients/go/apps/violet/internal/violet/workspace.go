package violet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/love-lena/sextant/clients/go/conventions/goals"
	"github.com/love-lena/sextant/protocol/wire"
)

// artifactReader is the slice of the SDK the deep refresh needs to assemble the
// live workspace state. The real *sextant.Client satisfies it; a fake satisfies
// it in tests, so the refresh path is exercised without a bus.
type artifactReader interface {
	ListArtifacts(ctx context.Context) ([]artifactInfo, error)
	GetArtifact(ctx context.Context, name string) (artifactValue, error)
}

// artifactInfo / artifactValue are the reader's view of an artifact — name +
// record, enough to find review-flagged work and read goals. They mirror the
// SDK's ArtifactInfo / Artifact without coupling this package to its concrete
// types (so the fake stays trivial).
type artifactInfo struct {
	Name     string
	Revision uint64
}

type artifactValue struct {
	Name     string
	Record   json.RawMessage
	Revision uint64
}

// gatheredWorkspace is the live state the wrapper assembles for one deep pass:
// the review queue (artifacts a producer flagged for the operator), the goal
// criteria and where they stand, and a digest of everything else. It is what the
// home-manager curates and summarizes — and what the wrapper uses to write the
// curated home projection.
type gatheredWorkspace struct {
	reviewQueue []reviewItem
	goals       []goalDigest
	otherCount  int
}

type reviewItem struct {
	Name     string
	Revision uint64
	State    string
	Title    string         // best-effort: a human label pulled from the record if present
	Relates  []goals.Relate // relations parsed from the record's `relates` array (goals.ParseRelates)
}

type goalDigest struct {
	Name     string
	Headline string // the goal's north-star (goals.Goal.Northstar) — never a `title` fallback
	Criteria []criterionDigest
}

type criterionDigest struct {
	Text   string // the criterion's text (goals.Criterion.Text) — never a `label` fallback
	Status string // the effective status after the proof-filter (goals.EffectiveStatus)
}

// gatherWorkspace reads the artifact directory and assembles the live state. It
// is a bounded, read-only sweep — list once, then get each artifact — that the
// deep pass runs (never the answer path). Failures on a single artifact are
// skipped (the bus owns these records; one bad read must not fail the pass).
//
// It reads every record once and keeps the raw records, because a goal's criteria
// statuses are read through the proof-filter (goals.EffectiveStatus): a criterion
// stored "met" reads as met only with a proof artifact backing it, and the proof
// lives in some OTHER artifact's relates. So goals are digested after the whole
// directory is in hand, with the proof set built from all records — the same
// invariant the dash applies, from the one conv/goals definition.
func gatherWorkspace(ctx context.Context, r artifactReader) (gatheredWorkspace, error) {
	infos, err := r.ListArtifacts(ctx)
	if err != nil {
		return gatheredWorkspace{}, fmt.Errorf("violet: list artifacts: %w", err)
	}
	type goalRec struct {
		name string
		goal goals.Goal
	}
	var ws gatheredWorkspace
	var goalRecs []goalRec
	var allRecords []json.RawMessage // for the proof-filter (proof relations live across artifacts)
	for _, info := range infos {
		art, err := r.GetArtifact(ctx, info.Name)
		if err != nil {
			continue // skip a single unreadable artifact; never fail the pass
		}
		var rec map[string]json.RawMessage
		if err := json.Unmarshal(art.Record, &rec); err != nil {
			ws.otherCount++
			continue
		}
		allRecords = append(allRecords, art.Record)
		// Goal? (its record carries criteria.) Defer the digest until every record
		// is read, so the proof-filter sees the whole directory.
		if g, ok := goals.ParseGoal(art.Record); ok {
			goalRecs = append(goalRecs, goalRec{name: info.Name, goal: g})
			continue
		}
		// Review-flagged? (a producer set review.state=review.)
		if state := reviewState(rec); state == "review" {
			ws.reviewQueue = append(ws.reviewQueue, reviewItem{
				Name:     info.Name,
				Revision: art.Revision,
				State:    state,
				Title:    recordTitle(rec),
				Relates:  goals.ParseRelates(art.Record),
			})
			continue
		}
		ws.otherCount++
	}
	for _, gr := range goalRecs {
		ws.goals = append(ws.goals, digestGoal(gr.name, gr.goal, allRecords))
	}
	sort.Slice(ws.reviewQueue, func(i, j int) bool { return ws.reviewQueue[i].Name < ws.reviewQueue[j].Name })
	sort.Slice(ws.goals, func(i, j int) bool { return ws.goals[i].Name < ws.goals[j].Name })
	return ws, nil
}

// digestGoal turns a parsed goal into the violet digest, reading its headline from
// the north-star (never a `title` fallback) and each criterion's text and
// proof-filtered status from the lexicon fields (never a `label`/`state`
// fallback). goalID is the artifact name "goal.<id>"; the proof set is built from
// allRecords for the goal's own id. This is the whole READ half — one shape, one
// source — replacing parseGoal's defensive field-name guessing that masked the
// dash↔violet drift.
func digestGoal(goalID string, g goals.Goal, allRecords []json.RawMessage) goalDigest {
	proved := goals.ProvedCriteria(strings.TrimPrefix(goalID, "goal."), allRecords)
	d := goalDigest{Name: goalID, Headline: g.Northstar}
	for _, c := range g.Criteria {
		d.Criteria = append(d.Criteria, criterionDigest{
			Text:   c.Text,
			Status: goals.EffectiveStatus(c, proved),
		})
	}
	return d
}

// reviewState reads the review-state convention block (dashapi/review.go): a
// `review` object inside the record with a `state` field. Absent ⇒ neutral.
func reviewState(rec map[string]json.RawMessage) string {
	raw, ok := rec["review"]
	if !ok {
		return ""
	}
	var rb struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &rb) != nil {
		return ""
	}
	return rb.State
}

// recordTitle pulls a best-effort human label from common record fields, for the
// review-queue digest. Falls back to the artifact name (the caller has it).
func recordTitle(rec map[string]json.RawMessage) string {
	for _, key := range []string{"title", "heading", "name", "summary"} {
		if raw, ok := rec[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}

// renderForCuration renders the gathered state into the text the home-manager
// curates: the review queue, the goals criterion-by-criterion, and the count of
// everything else. Deterministic ordering keeps it cache-stable turn to turn
// where nothing changed.
func (ws gatheredWorkspace) renderForCuration() string {
	var b strings.Builder
	b.WriteString("GOALS:\n")
	if len(ws.goals) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, g := range ws.goals {
		fmt.Fprintf(&b, "  %s — %s\n", g.Name, g.Headline)
		for _, c := range g.Criteria {
			fmt.Fprintf(&b, "    - [%s] %s\n", c.Status, c.Text)
		}
	}
	b.WriteString("REVIEW QUEUE (artifacts a producer flagged review):\n")
	if len(ws.reviewQueue) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, it := range ws.reviewQueue {
		title := it.Title
		if title == "" {
			title = it.Name
		}
		fmt.Fprintf(&b, "  %s (rev %d): %s\n", it.Name, it.Revision, title)
	}
	fmt.Fprintf(&b, "OTHER (context/working artifacts, not candidates): %d\n", ws.otherCount)
	return b.String()
}

// downstreamScore returns the number of goal criteria this item is a proof for.
// A higher score means this item blocks more downstream work — rank it first.
// What counts as proof is goals.Relate.IsProof — the one shared definition, so
// the ranking can't drift from the dash's approve loop or the read-side filter.
func downstreamScore(it reviewItem) int {
	return len(goals.Proofs(it.Relates))
}

// rankReviewQueue sorts items by blocks-most-downstream-work (descending). Items
// with the most proof relations to goal criteria rank first — they unblock the
// most downstream work. Ties are broken by name (lexicographic) for determinism.
func rankReviewQueue(items []reviewItem) []reviewItem {
	ranked := make([]reviewItem, len(items))
	copy(ranked, items)
	sort.SliceStable(ranked, func(i, j int) bool {
		si, sj := downstreamScore(ranked[i]), downstreamScore(ranked[j])
		if si != sj {
			return si > sj
		}
		return ranked[i].Name < ranked[j].Name
	})
	return ranked
}

// whyText produces the "why you're seeing this" rationale for a home agenda item.
// If the item has proof relations, it names the downstream goal criteria it
// unblocks. Otherwise it falls back to a terse needs-review line.
func whyText(it reviewItem, ws gatheredWorkspace) string {
	proofs := goals.Proofs(it.Relates) // the one shared proof definition
	if len(proofs) == 0 {
		return "[[" + it.Name + "]] needs your review."
	}
	goalHeadlines := make(map[string]string, len(ws.goals))
	for _, g := range ws.goals {
		goalHeadlines[g.Name] = g.Headline
	}
	if len(proofs) == 1 {
		p := proofs[0]
		goal := p.Goal
		if h := goalHeadlines["goal."+goal]; h != "" {
			goal = h
		}
		if p.Crit != "" {
			return "[[" + it.Name + "]] unblocks " + p.Crit + " on " + goal + "."
		}
		return "[[" + it.Name + "]] is proof for " + goal + " — your approval advances the goal."
	}
	return "[[" + it.Name + "]] unblocks " + itoa(len(proofs)) + " criteria across " + itoa(len(uniqueGoals(proofs))) + " goals."
}

func uniqueGoals(proofs []goals.Relate) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range proofs {
		if !seen[p.Goal] {
			seen[p.Goal] = true
			out = append(out, p.Goal)
		}
	}
	return out
}

// homeAgendaItem is one item in the curated agenda block — the structured "why
// you're seeing this" shape the dash reads as a field (home.jsx AgendaCall).
type homeAgendaItem struct {
	Action string `json:"action,omitempty"` // e.g. "review", "decide", "sign-off"
	Ref    string `json:"ref,omitempty"`    // artifact name or goal.<id>
	Text   string `json:"text"`             // "why you're seeing this" — the rationale
	Tone   string `json:"tone,omitempty"`   // "review", "call", "context"
}

// homeProjection is the curated `home` artifact record the dash reads
// (internal/dashapi/web/app/home.jsx): a greeting note (the curated state line)
// and blocks (agenda + pinned). The wrapper writes this; the home-manager
// supplied the judgement (which calls are real, the state line) via its snapshot,
// and the wrapper ranks the review queue into the agenda and pinned blocks.
type homeProjection struct {
	Type     string       `json:"$type"`
	Greeting homeGreeting `json:"greeting"`
	Blocks   []homeBlock  `json:"blocks"`
}

type homeGreeting struct {
	Heading string `json:"heading"`
	Note    string `json:"note"`
}

type homeBlock struct {
	Type  string           `json:"type"`
	Names []string         `json:"names,omitempty"`
	Items []homeAgendaItem `json:"items,omitempty"`
	Title string           `json:"title,omitempty"`
}

// marshal renders the projection as a wire.Lexicon for CreateArtifact/UpdateArtifact.
func (h homeProjection) marshal() wire.Lexicon {
	b, _ := json.Marshal(h)
	return wire.Lexicon(b)
}
