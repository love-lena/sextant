//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDashServeAPI is the D1 acceptance (TASK-68, ADR-0032) driven through the
// built binary: `sextant dash --serve` exposes a local HTTP API on 127.0.0.1
// behind a per-launch token, with the Go process the single bus client. It
// proves the token gate, JSON parity with the CLI read commands, the publish
// command path, and the SSE live stream — the same checks the self-validating
// demo runs, here enforced in CI.
func TestDashServeAPI(t *testing.T) {
	h := newHarness(t)
	h.startBus()

	// A second registered client so the directory has more than just the dash,
	// making the clients-parity check meaningful.
	if _, code := h.run(nil, "clients", "register", "peer", "--kind", "worker", "--store", h.store); code != 0 {
		t.Fatalf("register peer exited %d", code)
	}

	// `dash --serve`: zero-config first run self-enrolls the human seat, claims
	// the still-unclaimed principal (ADR-0031), and serves the local API.
	dash := h.startBg(nil, "dash", "--serve", "--store", h.store, "--port", "0")
	urlLine := dash.waitStdout(t, "token=")
	m := regexp.MustCompile(`(http://127\.0\.0\.1:\d+)/\?token=(\S+)`).FindStringSubmatch(urlLine)
	if m == nil {
		t.Fatalf("no serve URL in stdout line %q", urlLine)
	}
	base, token := m[1], m[2]

	// --- token gate ----------------------------------------------------------
	if code := getStatus(t, base+"/api/self"); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/self without token = %d, want 401", code)
	}

	// --- self: the dash claimed the principal on first run -------------------
	var self struct {
		ID        string `json:"id"`
		Principal string `json:"principal"`
	}
	apiGet(t, base+"/api/self", token, &self)
	if self.ID == "" {
		t.Fatal("/api/self returned empty id")
	}
	if self.Principal != self.ID {
		t.Fatalf("principal = %q, want the dash's own id %q (claimed on first run)", self.Principal, self.ID)
	}

	// --- publish (command) then read parity: API vs CLI ----------------------
	apiPost(t, base+"/api/publish", token, `{"subject":"msg.topic.e2e","record":{"$type":"chat.message","text":"hello-api"}}`)

	var msgs struct {
		Messages []struct {
			Record json.RawMessage `json:"record"`
		} `json:"messages"`
	}
	apiGet(t, base+"/api/messages?subject=msg.topic.e2e", token, &msgs)
	if !containsText(msgs.Messages, "hello-api") {
		t.Fatalf("/api/messages missing the published message: %+v", msgs)
	}
	// The CLI, reading the same bus as the same (active) identity, sees it too.
	cliRead, code := h.run(nil, "read", "msg.topic.e2e", "--store", h.store, "--json")
	if code != 0 || !strings.Contains(cliRead, "hello-api") {
		t.Fatalf("CLI read parity failed (code %d): %s", code, cliRead)
	}

	// --- clients parity: API vs CLI ------------------------------------------
	var apiClients []struct {
		DisplayName string `json:"DisplayName"`
	}
	apiGet(t, base+"/api/clients", token, &apiClients)
	apiNames := map[string]bool{}
	for _, c := range apiClients {
		apiNames[c.DisplayName] = true
	}
	if !apiNames["peer"] || len(apiNames) < 2 {
		t.Fatalf("/api/clients = %v, want at least the dash + peer", apiNames)
	}
	cliList, code := h.run(nil, "clients", "list", "--store", h.store, "--json")
	if code != 0 {
		t.Fatalf("clients list exited %d", code)
	}
	var cliClients []struct {
		DisplayName string `json:"DisplayName"`
	}
	if err := json.Unmarshal([]byte(cliList), &cliClients); err != nil {
		t.Fatalf("decode cli clients: %v\n%s", err, cliList)
	}
	if len(cliClients) != len(apiClients) {
		t.Fatalf("client count: api %d vs cli %d", len(apiClients), len(cliClients))
	}

	// --- SSE live stream: publish, assert it arrives -------------------------
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/stream?subject=msg.topic.live&token="+token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream content-type = %q", ct)
	}
	// A reader started before the publish so the live frame can't be missed.
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if ln := sc.Text(); strings.HasPrefix(ln, "data: ") {
				got <- ln
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond) // let the subscription register
	apiPost(t, base+"/api/publish", token, `{"subject":"msg.topic.live","record":{"$type":"chat.message","text":"live-frame"}}`)
	select {
	case ln := <-got:
		if !strings.Contains(ln, "live-frame") {
			t.Fatalf("stream data did not carry the published frame: %q", ln)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("published frame never arrived on the SSE stream")
	}
}

func containsText(msgs []struct {
	Record json.RawMessage `json:"record"`
}, want string,
) bool {
	for _, m := range msgs {
		if strings.Contains(string(m.Record), want) {
			return true
		}
	}
	return false
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func apiGet(t *testing.T, url, token string, v any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func apiPost(t *testing.T, url, token, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s = %d: %s", url, resp.StatusCode, b)
	}
}
