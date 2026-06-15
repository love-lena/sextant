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
		io.WriteString(w, `{"content":[{"type":"text","text":"  rebuilding the dash UI\n"}]}`)
	}))
	defer srv.Close()

	c := statushook.HaikuClient{APIKey: "test-key", Model: "claude-haiku-test", BaseURL: srv.URL, HTTP: srv.Client()}
	got, err := c.Status(context.Background(), "assistant: rebuilding the dash UI [tool: Bash]")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != "rebuilding the dash UI" { // trimmed
		t.Errorf("Status = %q, want %q", got, "rebuilding the dash UI")
	}
	if gotBody["model"] != "claude-haiku-test" {
		t.Errorf("request model = %v, want claude-haiku-test", gotBody["model"])
	}
	if _, ok := gotBody["max_tokens"]; !ok {
		t.Error("request missing max_tokens")
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
