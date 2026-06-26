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

// States is the agent.status state enum (TASK-84, lena's call): idle | working |
// waiting-for-human | waiting-for-agent | blocked (HARD, needs help) | done.
var States = []string{"idle", "working", "waiting-for-human", "waiting-for-agent", "blocked", "done"}

// ValidState reports whether s is a known state.
func ValidState(s string) bool {
	for _, v := range States {
		if v == s {
			return true
		}
	}
	return false
}

// StatusResult is the structured status the hook writes to the agent.status
// record: a coarse state from the enum plus a one-line headline.
type StatusResult struct {
	State    string
	Headline string
}

// ParseStatusLine decodes the model's "state | headline" reply. Fallbacks never
// drop the headline: no pipe ⇒ the whole line is the headline at state "working";
// an unrecognized state ⇒ "working".
func ParseStatusLine(s string) StatusResult {
	s = strings.TrimSpace(s)
	state, headline := "working", s
	if i := strings.IndexByte(s, '|'); i >= 0 {
		st := strings.TrimSpace(s[:i])
		headline = strings.TrimSpace(s[i+1:])
		if ValidState(st) {
			state = st
		}
	}
	return StatusResult{State: state, Headline: headline}
}

// statusSystemPrompt keeps the model on the "state | headline" contract.
const statusSystemPrompt = "You report an AI agent's CURRENT status for a team dashboard. " +
	"Given the agent's recent activity, classify its state and write a terse headline. " +
	"Reply with EXACTLY one line in the form: <state> | <headline>\n" +
	"<state> is one of: idle, working, waiting-for-human, waiting-for-agent, blocked, done " +
	"(blocked = HARD blocked, needs help; the waiting-* states are soft waits). " +
	"<headline> is a present-tense line (<= 12 words) of what it is doing right now — " +
	"e.g. 'implementing the dash history fix', 'awaiting lena's review'. No preamble, no trailing punctuation."

// HaikuClient is a minimal Anthropic Messages API client for one-line statuses.
// BaseURL defaults to the public API; tests point it at a mock. HTTP defaults to
// a client with a short timeout (the hook must never hang a turn).
type HaikuClient struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// Status asks Haiku to classify the agent's state + headline from the activity
// digest, returning the parsed StatusResult.
func (c HaikuClient) Status(ctx context.Context, activity string) (StatusResult, error) {
	if c.APIKey == "" {
		return StatusResult{}, errors.New("statushook: no ANTHROPIC_API_KEY")
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
		return StatusResult{}, fmt.Errorf("statushook: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return StatusResult{}, fmt.Errorf("statushook: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := hc.Do(req)
	if err != nil {
		return StatusResult{}, fmt.Errorf("statushook: call Haiku: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return StatusResult{}, fmt.Errorf("statushook: Haiku HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return StatusResult{}, fmt.Errorf("statushook: decode Haiku response: %w", err)
	}
	for _, b := range out.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return ParseStatusLine(b.Text), nil
		}
	}
	return StatusResult{}, errors.New("statushook: Haiku returned no text")
}
