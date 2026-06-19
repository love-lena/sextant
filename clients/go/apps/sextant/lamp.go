package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// The lamp is a small piece of ambient warmth on the bus: a `document` artifact
// named `lamp`. It doesn't power anything — it's just on (or off). First run
// places one; every subsequent run toggles. The toggle is a deliberately small
// demonstration of artifact compare-and-set: read the current state, flip it,
// write back at the same revision. Forkable like any artifact — read it, edit
// the body, replace it; your bus, your lamp.

// lampArtName is the artifact every `sextant lamp` invocation acts on.
const lampArtName = "lamp"

const lampOnArt = "```\n" +
	"            .   *   .   *   .\n" +
	"         *       ___       *\n" +
	"           .    /   \\    .\n" +
	"        *      |     |      *\n" +
	"           .    \\___/    .\n" +
	"         *        |        *\n" +
	"            .     |     .\n" +
	"                  |\n" +
	"                 _|_\n" +
	"                |___|\n" +
	"```\n"

const lampOffArt = "```\n" +
	"                 ___\n" +
	"                /   \\\n" +
	"               |     |\n" +
	"                \\___/\n" +
	"                  |\n" +
	"                  |\n" +
	"                  |\n" +
	"                  |\n" +
	"                 _|_\n" +
	"                |___|\n" +
	"```\n"

// lampRecord renders the document artifact for a lamp in the given state. Kept
// pure (no I/O) so the on/off shape is testable without a bus.
func lampRecord(on bool) []byte {
	title := "Lamp (off)"
	art := lampOffArt
	state := "It's off right now."
	if on {
		title = "Lamp (on)"
		art = lampOnArt
		state = "It's on right now — a permanent low setting, doing nothing in particular."
	}
	rec, _ := json.Marshal(map[string]any{
		"$type": "document",
		"title": title,
		"body":  "A small ambient lamp on the bus. " + state + "\n\n" + art,
		"on":    on,
	})
	return rec
}

// lampState reads the on/off flag from an artifact record. A record with no
// `on` field defaults to on — the natural state of a freshly-placed lamp.
func lampState(rec wire.Lexicon) bool {
	var doc struct {
		On *bool `json:"on"`
	}
	if err := json.Unmarshal(rec, &doc); err != nil || doc.On == nil {
		return true
	}
	return *doc.On
}

func cmdLamp(args []string) {
	// Sub-verb is optional: bare `sextant lamp` toggles.
	var verb string
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		verb, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("lamp", flag.ExitOnError)
	cf := addConnFlags(fs)
	_ = fs.Parse(args)

	ctx := context.Background()
	c := cf.connect(ctx)
	defer c.Close()

	switch verb {
	case "", "toggle":
		lampToggle(ctx, c)
	case "show":
		lampShow(ctx, c)
	default:
		fatal("unknown lamp verb %q (toggle|show)", verb)
	}
}

func lampShow(ctx context.Context, c *sextant.Client) {
	a, err := c.GetArtifact(ctx, lampArtName)
	if err != nil {
		fmt.Println("no lamp on the bus yet (run `sextant lamp` to place one)")
		return
	}
	state := "off"
	if lampState(a.Record) {
		state = "on"
	}
	fmt.Printf("lamp is %s (rev %d)\n", state, a.Revision)
}

func lampToggle(ctx context.Context, c *sextant.Client) {
	// First run places a lamp; subsequent runs flip its state via CAS.
	a, err := c.GetArtifact(ctx, lampArtName)
	if err != nil {
		rev, cerr := c.CreateArtifact(ctx, lampArtName, lampRecord(true))
		if cerr != nil {
			fatal("place lamp (get also failed: %v): %v", err, cerr)
		}
		fmt.Printf("placed a lamp on the bus, on (rev %d)\n", rev)
		return
	}
	newOn := !lampState(a.Record)
	newRev, err := c.UpdateArtifact(ctx, lampArtName, lampRecord(newOn), a.Revision)
	if err != nil {
		fatal("toggle lamp: %v", err)
	}
	state := "off"
	if newOn {
		state = "on"
	}
	fmt.Printf("lamp is now %s (rev %d)\n", state, newRev)
}
