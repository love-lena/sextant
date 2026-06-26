// Command gohelper drives the GO review convention over the Go SDK against a real
// bus, for the live co-equality scenario (TASK-239 AC#3). The TS test spawns it as
// a subprocess so the SAME artifact's review block is written/read by both
// languages' review conventions on ONE bus, and the record shapes are asserted
// byte-identical.
//
// It is a TEST helper (it lives under conventions/review/ts/test), not a shipped
// tool: it exists so the co-equality proof drives the Go convention's real write
// path (review.SetReview: get -> compare-and-set), the exact peer of the TS verb,
// rather than a hand-rolled artifact write. The canonical record it prints uses
// protocol/conformance.Canonicalize — the same FORMAT.md rule the TS SDK's
// `canonical` reproduces — so "byte-identical" is a real cross-language claim.
//
// Modes (first arg):
//
//	seed       <name>                 create <name> with a fixed producer-marked doc record.
//	setreview  <name> <state> <by>    run review.SetReview to persist a verdict, then print
//	                                  the resulting canonical record to stdout.
//	read       <name>                 artifact.get <name>, print its canonical record to stdout.
//
// Connection is via env: SEXTANT_CREDS and SEXTANT_URL. The canonical record is
// printed as a single line to stdout; diagnostics go to stderr.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	review "github.com/love-lena/sextant/conventions/review/go"
	pconf "github.com/love-lena/sextant/protocol/conformance"
	conf "github.com/love-lena/sextant/sdk/conformance"
	sextant "github.com/love-lena/sextant/sdk/go"
)

// fixedNow is the verdict timestamp, so a Go-written and a TS-written review block
// carry the same `at` value and canonicalize identically. The TS side passes the
// same constant.
const fixedNow = "2026-06-19T00:00:00Z"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gohelper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gohelper <seed|setreview|read>")
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
			return fmt.Errorf("usage: gohelper seed <name>")
		}
		return seed(ctx, ops, args[1])
	case "setreview":
		if len(args) != 4 {
			return fmt.Errorf("usage: gohelper setreview <name> <state> <by>")
		}
		return setreview(ctx, ops, args[1], args[2], args[3])
	case "read":
		if len(args) != 2 {
			return fmt.Errorf("usage: gohelper read <name>")
		}
		return read(ctx, ops, args[1])
	default:
		return fmt.Errorf("unknown mode %q", args[0])
	}
}

// fixedDoc is the producer-marked document both languages seed, so the scenario
// starts from one shared shape (a doc awaiting review).
func fixedDoc() json.RawMessage {
	return json.RawMessage(`{"$type":"doc","title":"the brief","body":"the body","review":{"state":"review"}}`)
}

func seed(ctx context.Context, ops *conf.SDKOps, name string) error {
	if _, err := ops.CreateArtifact(ctx, name, fixedDoc()); err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	return nil
}

func setreview(ctx context.Context, ops *conf.SDKOps, name, state, by string) error {
	if _, err := review.SetReview(ctx, ops, review.SetReviewInput{Name: name, State: state, By: by, Now: fixedNow}); err != nil {
		return fmt.Errorf("set review: %w", err)
	}
	return read(ctx, ops, name)
}

// read prints the artifact's record as canonical JSON (the FORMAT.md rule, via
// protocol/conformance.Canonicalize) on a single stdout line — the form the TS side
// compares against byte-for-byte.
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
