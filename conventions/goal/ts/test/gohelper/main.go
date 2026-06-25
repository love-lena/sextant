// Command gohelper drives the GO goals convention over the Go SDK against a real
// bus, for the live co-equality scenario (TASK-175 AC#4). The TS test spawns it as
// a subprocess so the SAME goal artifact is written/read by both languages' goals
// conventions on ONE bus, and the record shapes are asserted byte-identical.
//
// It is a TEST helper (it lives under conventions/goal/ts/test), not a
// shipped tool: it exists so the co-equality proof drives the Go convention's real
// write path (goals.SetCriterion: get → compare-and-set → publish goal.update),
// the exact peer of the TS verb, rather than a hand-rolled artifact write. The
// canonical record it prints uses protocol/conformance.Canonicalize — the same
// FORMAT.md rule the TS SDK's `canonical` reproduces — so "byte-identical" is a
// real cross-language claim.
//
// Modes (first arg):
//
//	seed  <goalId>            create goal.<goalId> with a fixed two-criterion record.
//	set   <goalId> <crit> <status> <headline>
//	                          run goals.SetCriterion to flip a criterion (the Go
//	                          convention's write path), then print the resulting
//	                          canonical goal record to stdout.
//	read  <name>             artifact.get <name>, print its canonical record to stdout.
//
// Connection is via env: SEXTANT_CREDS (path to the .creds file) and SEXTANT_URL
// (the bus NATS URL). The canonical record is printed as a single line to stdout;
// diagnostics go to stderr.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/love-lena/sextant/conventions/goal/go"
	pconf "github.com/love-lena/sextant/protocol/conformance"
	conf "github.com/love-lena/sextant/sdk/conformance"
	sextant "github.com/love-lena/sextant/sdk/go"
)

// fixedNow is the timestamp the goal.update / goal record stamps, so a Go-written
// record and a TS-written record carry the same `updated` value and canonicalize
// identically. The TS side passes the same constant.
const fixedNow = "2026-06-19T00:00:00Z"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gohelper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gohelper <seed|set|read>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath: os.Getenv("SEXTANT_CREDS"),
		URL:       os.Getenv("SEXTANT_URL"),
	})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = c.Close() }()
	ops := conf.NewSDKOps(c)

	switch args[0] {
	case "seed":
		if len(args) != 2 {
			return fmt.Errorf("usage: gohelper seed <goalId>")
		}
		return seed(ctx, ops, args[1])
	case "set":
		if len(args) != 5 {
			return fmt.Errorf("usage: gohelper set <goalId> <crit> <status> <headline>")
		}
		return set(ctx, ops, args[1], args[2], args[3], args[4])
	case "read":
		if len(args) != 2 {
			return fmt.Errorf("usage: gohelper read <name>")
		}
		return read(ctx, ops, args[1])
	default:
		return fmt.Errorf("unknown mode %q", args[0])
	}
}

// fixedGoal is the goal record both languages seed, so the scenario starts from
// one shared shape. The criterion the scenario flips (c1) starts not-started; the
// sibling (c2) is in-progress.
func fixedGoal() goals.Goal {
	return goals.Goal{
		Northstar: "Prove the goals convention is co-equal across languages",
		Stream:    "m6",
		Criteria: []goals.Criterion{
			{ID: "c1", Text: "TS and Go write the same goal record", Status: goals.StatusNotStarted, Owner: "sirius"},
			{ID: "c2", Text: "byte-identical canonical bytes", Status: goals.StatusInProgress},
		},
		Updated: fixedNow,
	}
}

func seed(ctx context.Context, ops *conf.SDKOps, goalID string) error {
	record, err := json.Marshal(fixedGoal())
	if err != nil {
		return err
	}
	if _, err := ops.CreateArtifact(ctx, goals.ArtifactName(goalID), record); err != nil {
		return fmt.Errorf("create %s: %w", goals.ArtifactName(goalID), err)
	}
	return nil
}

func set(ctx context.Context, ops *conf.SDKOps, goalID, crit, status, headline string) error {
	changed, err := goals.SetCriterion(ctx, ops, goals.SetCriterionInput{
		GoalID:      goalID,
		CriterionID: crit,
		Status:      status,
		Headline:    headline,
		By:          "go-helper",
	}, fixedNow)
	if err != nil {
		return fmt.Errorf("set criterion: %w", err)
	}
	if !changed {
		return fmt.Errorf("set criterion was a no-op (criterion %q absent or already %q)", crit, status)
	}
	return read(ctx, ops, goals.ArtifactName(goalID))
}

// read prints the artifact's record as canonical JSON (the FORMAT.md rule, via
// protocol/conformance.Canonicalize) on a single stdout line — the form the TS
// side compares against byte-for-byte.
func read(ctx context.Context, ops *conf.SDKOps, name string) error {
	record, _, err := ops.GetArtifact(ctx, name)
	if err != nil {
		return fmt.Errorf("get %s: %w", name, err)
	}
	canon, err := pconf.Canonicalize(record)
	if err != nil {
		return fmt.Errorf("canonicalize %s: %w", name, err)
	}
	fmt.Println(string(canon))
	return nil
}
