package statushook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/love-lena/sextant/internal/statushook"
)

// Status posts the activity digest to the Anthropic Messages API and returns the
// model's one-line status. The test mocks the API: it asserts the request shape
// (auth headers, endpoint, the activity in the body) and returns a canned reply.
func TestHaikuStatus(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing/wrong x-api-key: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		if !strings.Contains(string(b), "rebuilding the dash UI") {
			t.Errorf("request body missing the activity digest:\n%s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		// The model replies in the "state | headline" contract.
		io.WriteString(w, `{"content":[{"type":"text","text":"  working | rebuilding the dash UI\n"}]}`)
	}))
	defer srv.Close()

	c := statushook.HaikuClient{APIKey: "test-key", Model: "claude-haiku-test", BaseURL: srv.URL, HTTP: srv.Client()}
	got, err := c.Status(context.Background(), "assistant: rebuilding the dash UI [tool: Bash]")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != "working" {
		t.Errorf("State = %q, want working", got.State)
	}
	if got.Headline != "rebuilding the dash UI" { // trimmed
		t.Errorf("Headline = %q, want %q", got.Headline, "rebuilding the dash UI")
	}
	if gotBody["model"] != "claude-haiku-test" {
		t.Errorf("request model = %v, want claude-haiku-test", gotBody["model"])
	}
	if _, ok := gotBody["max_tokens"]; !ok {
		t.Error("request missing max_tokens")
	}
}

// ParseStatusLine decodes the model's "state | headline" contract, with safe
// fallbacks: no pipe ⇒ the whole line is the headline at state "working"; an
// unrecognized state ⇒ "working" (never drop the headline).
func TestParseStatusLine(t *testing.T) {
	cases := []struct{ in, state, head string }{
		{"working | rebuilding the dash UI", "working", "rebuilding the dash UI"},
		{"waiting-for-human | awaiting lena's review", "waiting-for-human", "awaiting lena's review"},
		{"waiting-for-agent | awaiting sirius gate", "waiting-for-agent", "awaiting sirius gate"},
		{"blocked|CI is red", "blocked", "CI is red"},
		{"just a headline, no pipe", "working", "just a headline, no pipe"},
		{"bogus-state | doing things", "working", "doing things"},
	}
	for _, c := range cases {
		got := statushook.ParseStatusLine(c.in)
		if got.State != c.state || got.Headline != c.head {
			t.Errorf("ParseStatusLine(%q) = {%q,%q}, want {%q,%q}", c.in, got.State, got.Headline, c.state, c.head)
		}
	}
}

// An empty API key is a clean error (the hook degrades to silent), not a panic.
func TestHaikuStatusNoKey(t *testing.T) {
	c := statushook.HaikuClient{APIKey: "", Model: "m"}
	if _, err := c.Status(context.Background(), "x"); err == nil {
		t.Error("expected an error with no API key")
	}
}

// A non-200 from the API is surfaced as an error, not a bogus status.
func TestHaikuStatusAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()
	c := statushook.HaikuClient{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Status(context.Background(), "x"); err == nil {
		t.Error("expected an error on a 429 response")
	}
}
