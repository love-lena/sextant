package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSanitizeName pins the defence against a model returning more than a bare
// handle: a sentence, quotes, punctuation, mixed case, or an out-of-range shape
// all normalize to a single safe lowercase handle or "" (which forces fallback).
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"atlas":            "atlas",
		"Atlas":            "atlas",
		"  kestrel  ":      "kestrel",
		`"cobalt"`:         "cobalt",
		"juno.":            "juno",
		"pike, the writer": "pike", // first token only
		"name: aurora":     "name", // first token only (model ignored instructions)
		"kestrel-2":        "kestrel-2",
		"has space":        "has", // first token only
		"":                 "",    // empty → fallback
		"a":                "",    // too short (needs >= 2)
		"123abc":           "",    // must start with a letter
	}

	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPickNameNoPicker: with no model configured (no API key), naming degrades
// straight to the safe fallback — never blocking the spawn.
func TestPickNameNoPicker(t *testing.T) {
	n := namer{pick: nil, list: nil}
	if got := n.pickName(context.Background(), "do a thing", "job", "agent-fallback"); got != "agent-fallback" {
		t.Fatalf("no picker should return fallback, got %q", got)
	}
}

// TestPickNameHappyPath: the model picks a name not in the directory; it is used.
func TestPickNameHappyPath(t *testing.T) {
	n := namer{
		pick: func(_ context.Context, _, _ string, _ []string) (string, error) { return "atlas", nil },
		list: func(context.Context) ([]string, error) { return []string{"sirius", "violet"}, nil },
	}
	if got := n.pickName(context.Background(), "write the press release", "", "agent-x"); got != "atlas" {
		t.Fatalf("happy path = %q, want atlas", got)
	}
}

// TestPickNameRetriesOnCollision: the first pick collides with an existing name;
// the namer feeds the taken set back and the second pick lands a unique name.
func TestPickNameRetriesOnCollision(t *testing.T) {
	var calls int
	var lastAvoid []string
	n := namer{
		pick: func(_ context.Context, _, _ string, avoid []string) (string, error) {
			calls++
			lastAvoid = avoid
			if calls == 1 {
				return "violet", nil // already taken
			}
			return "kestrel", nil
		},
		list: func(context.Context) ([]string, error) { return []string{"violet"}, nil },
	}
	got := n.pickName(context.Background(), "p", "", "agent-x")
	if got != "kestrel" {
		t.Fatalf("collision retry = %q, want kestrel", got)
	}
	if calls != 2 {
		t.Fatalf("expected 2 pick calls (1 collision + 1 success), got %d", calls)
	}
	// The retry must have been told to avoid the collided name.
	if !contains(lastAvoid, "violet") {
		t.Fatalf("retry avoid set %v should contain the collided name 'violet'", lastAvoid)
	}
}

// TestPickNameExhaustsToFallback: every pick collides; after the bounded attempts
// the namer falls back rather than looping forever.
func TestPickNameExhaustsToFallback(t *testing.T) {
	n := namer{
		pick: func(_ context.Context, _, _ string, _ []string) (string, error) { return "taken", nil },
		list: func(context.Context) ([]string, error) { return []string{"taken"}, nil },
	}
	if got := n.pickName(context.Background(), "p", "", "agent-safe"); got != "agent-safe" {
		t.Fatalf("exhausted attempts should fall back, got %q", got)
	}
}

// TestPickNamePickerErrorFallsBack: a model error never blocks the spawn.
func TestPickNamePickerErrorFallsBack(t *testing.T) {
	n := namer{
		pick: func(_ context.Context, _, _ string, _ []string) (string, error) {
			return "", errors.New("model down")
		},
		list: func(context.Context) ([]string, error) { return nil, nil },
	}
	if got := n.pickName(context.Background(), "p", "", "agent-safe"); got != "agent-safe" {
		t.Fatalf("picker error should fall back, got %q", got)
	}
}

// TestPickNameUnusableReplyFallsBack: a model reply that sanitizes to nothing
// (e.g. an apology sentence with no valid leading token) falls back.
func TestPickNameUnusableReplyFallsBack(t *testing.T) {
	n := namer{
		pick: func(_ context.Context, _, _ string, _ []string) (string, error) {
			return "404", nil // starts with a digit → invalid handle
		},
		list: func(context.Context) ([]string, error) { return nil, nil },
	}
	if got := n.pickName(context.Background(), "p", "", "agent-safe"); got != "agent-safe" {
		t.Fatalf("unusable reply should fall back, got %q", got)
	}
}

// TestPickNameListErrorStillNames: if the directory can't be read we cannot verify
// uniqueness, but a model name is still better than the opaque default — pick once.
func TestPickNameListErrorStillNames(t *testing.T) {
	n := namer{
		pick: func(_ context.Context, _, _ string, _ []string) (string, error) { return "atlas", nil },
		list: func(context.Context) ([]string, error) { return nil, errors.New("bus unreachable") },
	}
	if got := n.pickName(context.Background(), "p", "", "agent-x"); got != "atlas" {
		t.Fatalf("list error should still pick a name, got %q", got)
	}
}

// TestHaikuPickerParsesAPIReply exercises the real HTTP picker against a mock
// Anthropic endpoint: it sends the system + user prompt and parses the text reply.
func TestHaikuPickerParsesAPIReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k-test" {
			t.Errorf("missing api key header")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"kestrel\n"}]}`))
	}))
	defer srv.Close()

	pick := newHaikuPicker("k-test", srv.URL, srv.Client())
	if pick == nil {
		t.Fatal("picker should be non-nil with an api key")
	}
	got, err := pick(context.Background(), "write the release", "job", []string{"atlas"})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	// The picker returns the raw reply; sanitizeName trims it elsewhere.
	if sanitizeName(got) != "kestrel" {
		t.Fatalf("got %q, want kestrel after sanitize", got)
	}
}

// TestNewHaikuPickerNilWithoutKey: no API key → no picker (naming disabled).
func TestNewHaikuPickerNilWithoutKey(t *testing.T) {
	if newHaikuPicker("", "", nil) != nil {
		t.Fatal("expected nil picker without an api key")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
