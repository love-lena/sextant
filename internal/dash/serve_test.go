package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
)

// TestRunServeServesAPI is the serve-path integration: runServe connects under a
// real identity, binds a local listener, prints a URL carrying the per-launch
// token, and serves the API — and shuts down cleanly when its context is
// cancelled. It drives the whole `sextant dash --serve` glue against an embedded
// bus (CI-safe, default gate), short of the process boundary the demo covers.
func TestRunServeServesAPI(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	creds, dashID, err := b.MintClient(t.Context(), "dash", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsPath := writeCreds(t, creds)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, Options{
			CredsPath: credsPath,
			URL:       b.ClientURL(),
			Serve:     true,
			Addr:      "127.0.0.1:0", // ephemeral port — no conflicts in tests
		}, out)
	}()

	base, token := waitForServeURL(t, out)

	// /api/self over the real client returns this dash's bus identity.
	var self struct {
		ID string `json:"id"`
	}
	getJSON(t, base+"/api/self", token, &self)
	if self.ID != dashID {
		t.Fatalf("self id = %q, want minted dash id %q", self.ID, dashID)
	}

	// The bound listener is loopback-only.
	if !regexp.MustCompile(`^http://127\.0\.0\.1:`).MatchString(base) {
		t.Fatalf("served on %q, want a 127.0.0.1 address", base)
	}

	// Cancelling the context shuts the server down (no hang).
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServe did not return within 5s of context cancel")
	}
}

// waitForServeURL polls out until runServe has printed its URL line, returning
// the base (scheme://host:port) and the token query value.
func waitForServeURL(t *testing.T, out *syncBuffer) (base, token string) {
	t.Helper()
	re := regexp.MustCompile(`(http://[^/\s]+)/\?token=(\S+)`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m[1], m[2]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runServe never printed a URL line; output:\n%s", out.String())
	return "", ""
}

func getJSON(t *testing.T, url, token string, v any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer: runServe writes its announce line
// from its own goroutine while the test polls it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
