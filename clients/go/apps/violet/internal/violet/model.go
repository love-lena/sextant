package violet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Model ids violet drives (claude-api skill, 2026-06): a fast Haiku for the
// always-on gate and the conversational answers, a capable Sonnet for the deep
// home-manager refresh. The split is Lena's cost model — haiku triages every
// candidate, sonnet only runs when the gate wakes it.
const (
	ModelHaiku  = "claude-haiku-4-5"
	ModelSonnet = "claude-sonnet-4-6"

	anthropicVersion = "2023-06-01"
	defaultBaseURL   = "https://api.anthropic.com"
)

// turnRequest is one model turn: a frozen system prompt (cached) plus the warm
// conversation so far. The roles are single-turn from the client's view — the
// wrapper owns history and the publish, never the model (output-capture: the
// reply text IS the answer, the model has no publish tool to forget).
type turnRequest struct {
	System    string // the role prompt; stable across turns so it caches
	MaxTokens int
	Messages  []apiMessage
}

// apiMessage is one user/assistant turn in the Messages API shape.
type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// modelClient is a minimal Anthropic Messages API client. It mirrors
// internal/statushook (the established in-repo pattern) rather than pulling the
// full Anthropic Go SDK as a new dependency: violet makes single-turn,
// per-role calls, and a BaseURL seam lets the under-load test point it at a
// mock with controllable per-role latency — so the concurrency bar is exercised
// for real, not stubbed away.
type modelClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewModelClient builds a Messages-API client for violet's role turns. apiKey is
// the Anthropic key (violet's own — model access, distinct from her bus creds);
// baseURL is empty for the public API or a mock URL for tests; hc is optional.
func NewModelClient(apiKey, baseURL string, hc *http.Client) *modelClient {
	return newModelClient(apiKey, baseURL, hc)
}

// newModelClient builds a client. A per-call context governs cancellation; the
// HTTP client carries a generous ceiling so a wedged turn fails loud rather than
// hanging the role forever (fail-loud, never a silent hang).
func newModelClient(apiKey, baseURL string, hc *http.Client) *modelClient {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	return &modelClient{apiKey: apiKey, baseURL: baseURL, http: hc}
}

// turn runs one model turn and returns the assistant's text reply. The system
// prompt carries a cache_control breakpoint so the warm role prompt + injected
// context snapshot is served from cache on every subsequent turn (the API-side
// equivalent of the bash spike's warm session): turn 2+ pays cache-read rates
// and lands fast.
func (c *modelClient) turn(ctx context.Context, model string, req turnRequest) (string, error) {
	if c.apiKey == "" {
		return "", errors.New("violet: no ANTHROPIC_API_KEY")
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 1024
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": req.MaxTokens,
		// System as a single cached block: stable role prompt first, so the
		// prefix is byte-identical across turns and caches (claude-api skill,
		// prompt-caching). The volatile per-turn text rides in messages, after
		// the breakpoint.
		"system": []map[string]any{{
			"type":          "text",
			"text":          req.System,
			"cache_control": map[string]any{"type": "ephemeral"},
		}},
		"messages": req.Messages,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("violet: marshal turn: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("violet: build turn request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("violet: call model: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("violet: model HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("violet: decode model response: %w", err)
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", errors.New("violet: model returned no text")
	}
	return text, nil
}
