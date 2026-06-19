package dashapi_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// TestStreamPushesLiveFrames is the live-update contract: a frame the bus
// delivers on the subscribed subject is written to the SSE stream as a data
// event. This is the heart of D1 — watch the bus live in a browser.
func TestStreamPushesLiveFrames(t *testing.T) {
	bus := &fakeBus{}
	ts := httptest.NewServer(newServer(bus, "tok"))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/stream?subject=msg.topic.plan&token=tok", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// The subscription must be live before we push, or the frame is lost.
	waitFor(t, func() bool { return bus.activeSubs() == 1 })
	bus.push(sextant.Message{
		Subject:  "msg.topic.plan",
		Sequence: 5,
		Frame:    wire.Frame{ID: "01F1", Author: "01A", Kind: wire.KindMessage, Epoch: 1, Record: wire.Lexicon(`{"$type":"chat.message","text":"hi"}`)},
	})

	line := readDataLine(t, resp.Body, 2*time.Second)
	if !strings.Contains(line, `"id":"01F1"`) || !strings.Contains(line, `"sequence":5`) {
		t.Fatalf("stream data = %q", line)
	}
}

// TestStreamStopsSubscriptionOnDisconnect confirms a disconnecting client tears
// the bus subscription down — no relay leaks per closed browser tab.
func TestStreamStopsSubscriptionOnDisconnect(t *testing.T) {
	bus := &fakeBus{}
	ts := httptest.NewServer(newServer(bus, "tok"))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/stream?subject=msg.topic.x&token=tok", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	waitFor(t, func() bool { return bus.activeSubs() == 1 })

	cancel()
	resp.Body.Close()
	waitFor(t, func() bool { return bus.activeSubs() == 0 })
}

func TestStreamRequiresSubject(t *testing.T) {
	srv := newServer(&fakeBus{}, "tok")
	rec := get(srv, "/api/stream")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for missing subject (%s)", rec.Code, rec.Body.String())
	}
}

// waitFor polls cond until it holds or a deadline passes (fail-loud, never hang).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within deadline")
}

// readDataLine reads the next SSE `data:` payload from body, bounded by timeout.
func readDataLine(t *testing.T, body io.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				ch <- result{line: strings.TrimPrefix(line, "data: ")}
				return
			}
		}
		ch <- result{err: sc.Err()}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read stream: %v", r.err)
		}
		return r.line
	case <-time.After(timeout):
		t.Fatalf("no data line within %s", timeout)
		return ""
	}
}
