package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/love-lena/sextant/pkg/wire"
)

// TestLampRecordRoundTrip verifies that lampRecord emits a parseable document
// record whose `on` field round-trips through lampState. Pure shape check — no
// bus required.
func TestLampRecordRoundTrip(t *testing.T) {
	for _, want := range []bool{true, false} {
		rec := lampRecord(want)
		if !json.Valid(rec) {
			t.Fatalf("lampRecord(%v) is not valid JSON: %s", want, rec)
		}
		var doc struct {
			Type  string `json:"$type"`
			Title string `json:"title"`
			Body  string `json:"body"`
			On    bool   `json:"on"`
		}
		if err := json.Unmarshal(rec, &doc); err != nil {
			t.Fatalf("unmarshal lampRecord(%v): %v", want, err)
		}
		if doc.Type != "document" {
			t.Errorf("$type = %q, want \"document\" (other clients render documents)", doc.Type)
		}
		if doc.On != want {
			t.Errorf("on = %v, want %v", doc.On, want)
		}
		if got := lampState(wire.Lexicon(rec)); got != want {
			t.Errorf("lampState round-trip = %v, want %v", got, want)
		}
		// The body should visibly indicate the state — that's how someone
		// reading the artifact from a dash or the CLI knows it's on or off.
		stateWord := "off"
		if want {
			stateWord = "on"
		}
		if !strings.Contains(doc.Title, stateWord) {
			t.Errorf("title %q does not mention state %q", doc.Title, stateWord)
		}
	}
}

// TestLampStateDefaultsOn covers the legacy/forked case: an artifact named
// `lamp` exists but the record has no `on` field. A lamp without a switch is
// assumed lit — that's the natural state.
func TestLampStateDefaultsOn(t *testing.T) {
	for _, rec := range []string{
		`{"$type":"document","title":"Lamp","body":"hi"}`, // no on field
		`{}`, // empty record
		`not json`,
	} {
		if !lampState(wire.Lexicon(rec)) {
			t.Errorf("lampState(%q) = false, want true (missing-or-broken should default on)", rec)
		}
	}
}

// TestLampOnAndOffArtDiffer guards against accidentally collapsing the two
// renderings — the visible difference is what makes the toggle perceivable.
func TestLampOnAndOffArtDiffer(t *testing.T) {
	if lampOnArt == lampOffArt {
		t.Fatal("on/off art are identical; the toggle would be invisible")
	}
	if !strings.Contains(lampOnArt, "*") {
		t.Errorf("on-art should carry the warm-glow stippling (`*`); got %q", lampOnArt)
	}
	if strings.Contains(lampOffArt, "*") {
		t.Errorf("off-art should not carry glow; got %q", lampOffArt)
	}
}
