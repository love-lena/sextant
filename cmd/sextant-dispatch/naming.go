package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Haiku auto-naming (this slice). When a spawn.request arrives with no nickname,
// the dispatcher asks a cheap Haiku model to pick a unique, evocative name for the
// child before minting it — so a spawned agent gets a real handle ("atlas",
// "kestrel") instead of an opaque "agent-01H…". Naming is best-effort and NEVER
// blocks the spawn: any failure (no API key, a model error, an unparseable reply,
// or repeated collisions) falls back to the safe default name. The name is just a
// DisplayName the dispatcher mints the child under; it is unique by convention,
// not enforced by the bus (ADR-0020), so the dispatcher verifies uniqueness
// against the live clients directory itself.

const (
	// namingModel is the cheap model the namer calls. Haiku is the whole point —
	// naming is a tiny, low-stakes turn; it must be fast and cheap.
	namingModel = "claude-haiku-4-5"

	// namingMaxAttempts bounds the name turns: a fresh attempt only happens on a
	// collision (the picked name is already taken), feeding the taken set back so
	// the model avoids it. Past this, the spawn falls back rather than looping.
	namingMaxAttempts = 3

	// namingTimeout bounds each naming turn. Naming is on the spawn critical path,
	// so a wedged model call must fail loud and fall back, never hang the spawn.
	namingTimeout = 10 * time.Second
)

// nameLister returns the display names already taken in the clients directory, so
// the namer can verify uniqueness. It is the dispatcher's own ListClients in
// production; tests inject a stub.
type nameLister func(ctx context.Context) ([]string, error)

// namePicker runs one naming turn: given the task context and the set of names to
// avoid, it returns a candidate name. It is the Haiku call in production; tests
// inject a stub to exercise the collision/fallback logic without a model.
type namePicker func(ctx context.Context, prompt, job string, avoid []string) (string, error)

// namer turns a (prompt, job) into a unique child name, falling back to a safe
// default on any trouble. It composes a picker (the model) with a lister (the
// directory) so both halves are independently testable.
type namer struct {
	pick namePicker
	list nameLister
}

// pickName returns a unique, evocative name for a child, or the supplied fallback
// if naming can't safely produce one. It never returns an error: a failed naming
// is a logged degradation, not a failed spawn (fail-safe, never block the spawn).
func (n namer) pickName(ctx context.Context, prompt, job, fallback string) string {
	if n.pick == nil {
		return fallback // no model configured (no API key) — straight to the default
	}

	taken := map[string]bool{}
	if n.list != nil {
		names, err := n.list(ctx)
		if err != nil {
			// Can't read the directory: we can't guarantee uniqueness, but a
			// name is still better than the opaque default. Pick once, unverified.
			logf("naming: clients list failed (%v); picking a name without uniqueness check", err)
		} else {
			for _, nm := range names {
				taken[strings.ToLower(nm)] = true
			}
		}
	}

	avoid := mapKeys(taken)
	for attempt := 1; attempt <= namingMaxAttempts; attempt++ {
		turnCtx, cancel := context.WithTimeout(ctx, namingTimeout)
		raw, err := n.pick(turnCtx, prompt, job, avoid)
		cancel()
		if err != nil {
			logf("naming: pick turn failed (%v); falling back to %q", err, fallback)
			return fallback
		}
		name := sanitizeName(raw)
		if name == "" {
			logf("naming: model returned an unusable name %q; falling back to %q", raw, fallback)
			return fallback
		}
		if taken[strings.ToLower(name)] {
			// Collision: add it to the avoid set and try once more. The model
			// picked a name already in the directory; ask it for a different one.
			logf("naming: %q already taken; retrying (attempt %d/%d)", name, attempt, namingMaxAttempts)
			taken[strings.ToLower(name)] = true
			avoid = mapKeys(taken)
			continue
		}
		return name
	}
	logf("naming: exhausted %d attempts without a unique name; falling back to %q", namingMaxAttempts, fallback)
	return fallback
}

// nameRe is the conservative shape a child display name must satisfy: a short
// lowercase handle. It mirrors what reads well on the bus and in the directory
// ("atlas", "kestrel-2") and keeps a model from smuggling whitespace/punctuation
// into an identity label.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)

// sanitizeName normalizes a model's reply to a single safe handle, or "" if it
// can't. The model is told to reply with just the name, but we defend anyway:
// take the first token, lowercase it, strip stray punctuation, and validate.
func sanitizeName(raw string) string {
	s := strings.TrimSpace(raw)
	// First whitespace-delimited token only (defend against a sentence reply).
	if i := strings.IndexAny(s, " \t\n\r"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(strings.Trim(s, `"'.,:;!?`+"`"))
	if !nameRe.MatchString(s) {
		return ""
	}
	return s
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// haikuPicker is the production namePicker: a minimal Anthropic Messages API call
// (the established in-repo pattern, internal/statushook). apiKey is the
// dispatcher's own model-access key (distinct from its bus creds); baseURL is
// empty for the public API or a mock URL for tests.
type haikuPicker struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// newHaikuPicker builds a picker, or nil when no API key is configured — the namer
// then degrades straight to the fallback name (naming is optional).
func newHaikuPicker(apiKey, baseURL string, hc *http.Client) namePicker {
	if apiKey == "" {
		return nil
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if hc == nil {
		hc = &http.Client{Timeout: namingTimeout}
	}
	p := haikuPicker{apiKey: apiKey, baseURL: baseURL, http: hc}
	return p.pick
}

const namingVersion = "2023-06-01"

const namingSystem = "You name a newly spawned AI agent for a collaboration bus. " +
	"Given the task the agent will do, invent ONE short, evocative, memorable handle for it — " +
	"a single lowercase word (optionally with a digit suffix), 2 to 20 characters, " +
	"like 'atlas', 'kestrel', 'cobalt', 'juno', 'pike'. " +
	"Prefer a word that subtly evokes the task, but a clean neutral name is fine. " +
	"Reply with EXACTLY the name and nothing else: no quotes, no punctuation, no explanation."

func (p haikuPicker) pick(ctx context.Context, prompt, job string, avoid []string) (string, error) {
	var user strings.Builder
	user.WriteString("Task for the agent:\n")
	if strings.TrimSpace(prompt) == "" {
		user.WriteString("(no specific brief)")
	} else {
		user.WriteString(prompt)
	}
	if strings.TrimSpace(job) != "" {
		user.WriteString("\nJob label: ")
		user.WriteString(job)
	}
	if len(avoid) > 0 {
		user.WriteString("\nThese names are already taken — pick a DIFFERENT one: ")
		user.WriteString(strings.Join(avoid, ", "))
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":      namingModel,
		"max_tokens": 16,
		"system":     namingSystem,
		"messages":   []map[string]any{{"role": "user", "content": user.String()}},
	})
	if err != nil {
		return "", fmt.Errorf("naming: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("naming: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", namingVersion)

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("naming: call model: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("naming: model HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("naming: decode model response: %w", err)
	}
	for _, b := range out.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return b.Text, nil
		}
	}
	return "", errors.New("naming: model returned no text")
}
