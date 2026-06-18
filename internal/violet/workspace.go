package violet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/love-lena/sextant/pkg/wire"
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
	Title    string // best-effort: a human label pulled from the record if present
}

type goalDigest struct {
	Name     string
	Headline string
	Criteria []criterionDigest
}

type criterionDigest struct {
	Label  string
	Status string // e.g. "met", "waiting-on-you", "pending"
}

// gatherWorkspace reads the artifact directory and assembles the live state. It
// is a bounded, read-only sweep — list once, then get each artifact — that the
// deep pass runs (never the answer path). Failures on a single artifact are
// skipped (the bus owns these records; one bad read must not fail the pass).
func gatherWorkspace(ctx context.Context, r artifactReader) (gatheredWorkspace, error) {
	infos, err := r.ListArtifacts(ctx)
	if err != nil {
		return gatheredWorkspace{}, fmt.Errorf("violet: list artifacts: %w", err)
	}
	var ws gatheredWorkspace
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
		// Goal? (its record carries criteria.)
		if g, ok := parseGoal(info.Name, rec); ok {
			ws.goals = append(ws.goals, g)
			continue
		}
		// Review-flagged? (a producer set review.state=review.)
		if state := reviewState(rec); state == "review" {
			ws.reviewQueue = append(ws.reviewQueue, reviewItem{
				Name:     info.Name,
				Revision: art.Revision,
				State:    state,
				Title:    recordTitle(rec),
			})
			continue
		}
		ws.otherCount++
	}
	sort.Slice(ws.reviewQueue, func(i, j int) bool { return ws.reviewQueue[i].Name < ws.reviewQueue[j].Name })
	sort.Slice(ws.goals, func(i, j int) bool { return ws.goals[i].Name < ws.goals[j].Name })
	return ws, nil
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

// parseGoal recognises a goal record by its criteria array and digests each
// criterion's status, so the deep pass can surface waiting-on-you criteria and
// say where each goal stands. A record with no criteria array is not a goal.
func parseGoal(name string, rec map[string]json.RawMessage) (goalDigest, bool) {
	raw, ok := rec["criteria"]
	if !ok {
		return goalDigest{}, false
	}
	var crits []struct {
		Label  string `json:"label"`
		Text   string `json:"text"`
		Status string `json:"status"`
		State  string `json:"state"`
	}
	if json.Unmarshal(raw, &crits) != nil {
		return goalDigest{}, false
	}
	g := goalDigest{Name: name, Headline: recordTitle(rec)}
	for _, c := range crits {
		label := c.Label
		if label == "" {
			label = c.Text
		}
		status := c.Status
		if status == "" {
			status = c.State
		}
		g.Criteria = append(g.Criteria, criterionDigest{Label: label, Status: status})
	}
	return g, true
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
			fmt.Fprintf(&b, "    - [%s] %s\n", c.Status, c.Label)
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

// homeProjection is the curated `home` artifact record the dash reads
// (internal/dashapi/web/app/home.jsx): a greeting note (the curated state line)
// and a pinned block (the ranked real calls). The wrapper writes this; the
// home-manager supplied the judgement (which calls are real, the state line) via
// its snapshot, and the wrapper ranks the review queue into pinned names.
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
	Type  string   `json:"type"`
	Names []string `json:"names,omitempty"`
}

// marshal renders the projection as a wire.Lexicon for CreateArtifact/UpdateArtifact.
func (h homeProjection) marshal() wire.Lexicon {
	b, _ := json.Marshal(h)
	return wire.Lexicon(b)
}
