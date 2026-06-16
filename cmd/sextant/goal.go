package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/love-lena/sextant/pkg/sextant"
)

// goalStates is the goal.state enum (ADR-0035, protocol/lexicons/goal.json):
// pending | active | blocked | done | dropped. Coarse states that drive the dash
// Goals pill; the "why" lives in the headline/ref (content), not a sixth state.
var goalStates = []string{"pending", "active", "blocked", "done", "dropped"}

func validGoalState(s string) bool {
	for _, v := range goalStates {
		if v == s {
			return true
		}
	}
	return false
}

// goalsTopic is the single observable stream of goal transitions (ADR-0035).
const goalsTopic = "msg.topic.goals"

func goalName(id string) string { return "goal." + id }

// cmdGoal drives the goal primitive (TASK-84 / ADR-0035): declare or move a
// shared objective. `goal set` CAS-upserts the latest-value artifact goal.<id>
// AND publishes a goal.update signal on msg.topic.goals — self-report, the same
// model used for agent.status. `goal get` / `goal list` read. A goal SIGNALS;
// it does not manage — anyone may correct a goal artifact, the owner stays
// authoritative.
func cmdGoal(args []string) {
	if len(args) < 1 {
		fatal("usage: sextant goal set|get|list <id> [--state S] [--title T] [--headline H] [--progress P] [--owner O] [--ref R]")
	}
	switch args[0] {
	case "set":
		goalSet(args[1:])
	case "get":
		goalGet(args[1:])
	case "list":
		goalList(args[1:])
	default:
		fatal("usage: sextant goal set|get|list ...")
	}
}

func goalSet(args []string) {
	// <id> is the first positional (flags follow it; Go's flag package stops at
	// the first non-flag), or may come after flags.
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("goal set", flag.ExitOnError)
	state := fs.String("state", "", "goal state (required): pending|active|blocked|done|dropped")
	title := fs.String("title", "", "what the objective is (required when first declaring it)")
	headline := fs.String("headline", "", "short present-tense line of the latest movement")
	progress := fs.String("progress", "", "optional short progress, e.g. '3/5' or '60%'")
	owner := fs.String("owner", "", "optional agent accountable for the goal")
	ref := fs.String("ref", "", "optional ticket/PR/milestone this goal concerns")
	noSignal := fs.Bool("no-signal", false, "only update goal.<id>; do not publish the goal.update signal")
	cf := addConnFlags(fs)
	_ = fs.Parse(args)
	if id == "" {
		if rest := fs.Args(); len(rest) > 0 {
			id = rest[0]
		}
	}
	if id == "" {
		fatal("usage: sextant goal set <id> --state S [--title T] [--headline H] ...")
	}
	if !validGoalState(*state) {
		fatal("--state must be one of: %s", strings.Join(goalStates, " | "))
	}

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	by := c.ID()
	name := goalName(id)

	// Upsert: merge onto any existing record so an update needn't re-supply
	// title/owner/ref. CAS on the current revision.
	rec := map[string]any{"$type": "goal", "state": *state, "updated": now, "by": by}
	var prevRev uint64
	exists := false
	if art, err := c.GetArtifact(ctx, name); err == nil {
		exists, prevRev = true, art.Revision
		var prev map[string]any
		if json.Unmarshal(art.Record, &prev) == nil {
			for _, k := range []string{"title", "headline", "progress", "owner", "ref"} {
				if v, ok := prev[k]; ok {
					rec[k] = v
				}
			}
		}
	}
	setIf := func(k, v string) {
		if v != "" {
			rec[k] = v
		}
	}
	setIf("title", *title)
	setIf("headline", *headline)
	setIf("progress", *progress)
	setIf("owner", *owner)
	setIf("ref", *ref)
	if _, ok := rec["title"]; !ok {
		fatal("a goal needs a --title when first declared")
	}

	b, err := json.Marshal(rec)
	if err != nil {
		fatal("marshal goal: %v", err)
	}
	var newRev uint64
	if exists {
		newRev, err = c.UpdateArtifact(ctx, name, json.RawMessage(b), prevRev)
	} else {
		newRev, err = c.CreateArtifact(ctx, name, json.RawMessage(b))
	}
	if err != nil {
		fatal("write %s: %v", name, err)
	}

	// Signal the transition on the single goals stream (ADR-0035), unless suppressed.
	if !*noSignal {
		upd := map[string]any{"$type": "goal.update", "goal": id, "state": *state, "updated": now, "by": by}
		setUpd := func(k, v string) {
			if v != "" {
				upd[k] = v
			}
		}
		setUpd("headline", *headline)
		setUpd("progress", *progress)
		setUpd("ref", *ref)
		ub, err := json.Marshal(upd)
		if err != nil {
			fatal("marshal goal.update: %v", err)
		}
		if err := c.Publish(ctx, goalsTopic, json.RawMessage(ub)); err != nil {
			fatal("publish goal.update: %v", err)
		}
	}

	verb := "updated"
	if !exists {
		verb = "declared"
	}
	fmt.Printf("goal %s %s (revision %d) — state=%s\n", id, verb, newRev, *state)
}

func goalGet(args []string) {
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("goal get", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the goal record as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args)
	if id == "" {
		fatal("usage: sextant goal get <id> [--json]")
	}
	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	a, err := c.GetArtifact(ctx, goalName(id))
	if err != nil {
		fatal("%v", err)
	}
	if *asJSON {
		emitJSON(a)
		return
	}
	fmt.Printf("%s (revision %d)\n%s\n", a.Name, a.Revision, a.Record)
}

func goalList(args []string) {
	fs := flag.NewFlagSet("goal list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the goals as JSON")
	cf := addConnFlags(fs)
	_ = fs.Parse(args)
	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()
	arts, err := c.ListArtifacts(ctx)
	if err != nil {
		fatal("%v", err)
	}
	var goals []sextant.ArtifactInfo
	for _, a := range arts {
		if strings.HasPrefix(a.Name, "goal.") {
			goals = append(goals, a)
		}
	}
	sort.Slice(goals, func(i, j int) bool { return goals[i].Name < goals[j].Name })
	if *asJSON {
		emitJSON(goals)
		return
	}
	if len(goals) == 0 {
		fmt.Println("no goals yet — declare one: sextant goal set <id> --title T --state pending")
		return
	}
	for _, g := range goals {
		id := strings.TrimPrefix(g.Name, "goal.")
		state, line := "?", ""
		if a, err := c.GetArtifact(ctx, g.Name); err == nil {
			var r struct{ State, Headline, Progress string }
			if json.Unmarshal(a.Record, &r) == nil {
				state, line = r.State, r.Headline
				if r.Progress != "" {
					line = strings.TrimSpace(line + " (" + r.Progress + ")")
				}
			}
		}
		fmt.Printf("%-24s [%-8s] %s\n", id, state, line)
	}
}
