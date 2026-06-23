package components

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashserve"
)

// dashHealthBudget bounds the dash readiness probe: a freshly-kickstarted dash
// binds its listener and writes its state file within a few hundred ms, so a short
// budget catches a serve failure (the bind raced, the bus is wedged) without a long
// stall — fail-loud, never hang.
const (
	dashHealthBudget   = 5 * time.Second
	dashHealthInterval = 100 * time.Millisecond
)

// dashHealthy is the dash component's readiness probe (AC#2): launchd reporting the
// process "running" is not enough — a dash that crashed after launch, or whose bind
// raced, leaves a live-but-not-serving process. So it polls for the state file the
// dash writes once its listener is bound ($SEXTANT_HOME/dash.json), then GETs that
// URL and requires HTTP 200 before reporting healthy. It is bounded by
// dashHealthBudget so a never-serving dash fails loud rather than hanging the start.
func dashHealthy() error {
	deadline := time.Now().Add(dashHealthBudget)
	var lastErr error
	for {
		if err := dashServing(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the dash did not serve within %s (check its log: %s): %w", dashHealthBudget, LogPath("dash"), lastErr)
		}
		time.Sleep(dashHealthInterval)
	}
}

// dashServing reads the dash's state file and GETs its URL, returning nil only on
// an HTTP 200. A missing state file (not yet written) or a non-200 is an error the
// poll loop retries until the budget elapses.
func dashServing() error {
	state, err := dashserve.ReadStateFile(DashStateFile())
	if err != nil {
		return fmt.Errorf("read dash state file: %w", err)
	}
	if state.URL == "" {
		return fmt.Errorf("dash state file has no URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), dashHealthInterval)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, state.URL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET dash url: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dash url returned HTTP %d, want 200", resp.StatusCode)
	}
	return nil
}
