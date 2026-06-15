package statushook

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

// DefaultModel is the Haiku model the status hook calls — fast + cheap, the whole
// point of the design (AC#4). Overridable via the hook's --model / env.
const DefaultModel = "claude-haiku-4-5-20251001"

const anthropicVersion = "2023-06-01"

// statusSystemPrompt keeps the model on task: one terse present-tense line.
const statusSystemPrompt = "You report an AI agent's CURRENT status for a team dashboard. " +
	"Given the agent's recent activity, reply with ONE terse present-tense line (<= 12 words) " +
	"of what it is doing right now — e.g. 'implementing the dash history fix', 'waiting on review', " +
	"'running CI'. No preamble, no punctuation at the end, just the status."

// HaikuClient is a minimal Anthropic Messages API client for one-line statuses.
// BaseURL defaults to the public API; tests point it at a mock. HTTP defaults to
// a client with a short timeout (the hook must never hang a turn).
type HaikuClient struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// Status asks Haiku for a one-line status from the activity digest.
func (c HaikuClient) Status(ctx context.Context, activity string) (string, error) {
	if c.APIKey == "" {
		return "", errors.New("statushook: no ANTHROPIC_API_KEY")
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 64,
		"system":     statusSystemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": "Recent activity:\n" + activity},
		},
	})
	if err != nil {
		return "", fmt.Errorf("statushook: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("statushook: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("statushook: call Haiku: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("statushook: Haiku HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("statushook: decode Haiku response: %w", err)
	}
	for _, b := range out.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return strings.TrimSpace(b.Text), nil
		}
	}
	return "", errors.New("statushook: Haiku returned no text")
}
