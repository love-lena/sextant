package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/love-lena/sextant/clients/sextant-mcp/attest"
	"github.com/love-lena/sextant/clients/sextant-mcp/internal/statushook"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/sdk/go"
)

// status is the claude-code plugin's PostToolUse hook body (TASK-87): a per-agent
// self-status. The hook fires on every tool call, but is cheap — it only gates +
// throttles, and when it decides to fire it spawns a DETACHED worker that does the
// slow part (read transcript → Haiku → write status) so the agent's tool flow is
// never blocked.
//
// Gating (skip-if-not-connected): if this session has no per-session identity file
// (the one the MCP server writes on connect), the MCP never connected — this is a
// regular non-bus session — so the hook exits 0 immediately, no Haiku, no failure.
// Same mechanism as `attest`. Throttle: the Haiku call runs at most once per
// interval (default 45s), decoupling hook-fire-rate from Haiku-call-rate.
//
// PROTOTYPE scope (lena, 2026-06-14): the sextant write is STUBBED — the worker
// writes the status to a local file under CLAUDE_PLUGIN_DATA. Wiring it to a bus
// status primitive is the deferred sextant side (TASK-84).
//
// Discipline: like attest, it NEVER blocks or fails a turn — every path exits 0,
// diagnostics go to stderr.

const (
	statusIntervalEnv = "SEXTANT_STATUS_INTERVAL" // seconds; overrides the default throttle
	statusModelEnv    = "SEXTANT_STATUS_MODEL"
	statusAPIBaseEnv  = "SEXTANT_STATUS_API_BASE" // overrides the Anthropic base URL (tests/demos)
	apiKeyEnv         = "ANTHROPIC_API_KEY"
	defaultInterval   = 45 * time.Second
	workerBudget      = 10 * time.Second // bound the detached worker's whole run
	transcriptLines   = 30
)

// statusHookInput is the PostToolUse hook stdin contract (the fields we use).
type statusHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// runStatus is the hook entrypoint. args is os.Args[2:] (after "status").
func runStatus(args []string) int {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	fs := flag.NewFlagSet("sextant-mcp status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	worker := fs.Bool("worker", false, "internal: run the detached status worker")
	wSession := fs.String("session", "", "internal: session id (worker mode)")
	wTranscript := fs.String("transcript", "", "internal: transcript path (worker mode)")
	if err := fs.Parse(args); err != nil {
		return 0 // never block the turn on a flag slip
	}

	if *worker {
		runStatusWorker(*wSession, *wTranscript)
		return 0
	}

	// Hook mode: read stdin, decide, and (if firing) spawn the detached worker.
	var in statusHookInput
	if b, err := io.ReadAll(os.Stdin); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &in); err != nil {
			log.Printf("sextant-mcp status: undecodable hook stdin, skipping: %v", err)
			return 0
		}
	}

	dataDir := os.Getenv("CLAUDE_PLUGIN_DATA")
	fire, reason := decideFire(dataDir, os.Getenv(sessionEnv), in.SessionID, time.Now(), statusInterval())
	if !fire {
		log.Printf("sextant-mcp status: skip (%s)", reason)
		return 0
	}
	if in.TranscriptPath == "" {
		log.Printf("sextant-mcp status: no transcript_path; skipping")
		return 0
	}
	if err := spawnWorker(in.SessionID, in.TranscriptPath); err != nil {
		log.Printf("sextant-mcp status: spawn worker: %v (skipping)", err)
	}
	return 0
}

// decideFire applies the two gates — connected? then throttled? — and, when it
// returns true, advances the throttle state (so a concurrent/next fire is gated).
// It is the testable core: pure file IO, no bus, no network.
func decideFire(dataDir, identitySession, stateSession string, now time.Time, interval time.Duration) (bool, string) {
	if dataDir == "" {
		return false, "no CLAUDE_PLUGIN_DATA"
	}
	// Skip-if-not-connected: no identity file ⇒ the MCP server never connected this
	// session ⇒ a regular non-bus session ⇒ do nothing (no Haiku, no failure).
	if _, err := attest.LoadIdentity(dataDir, identitySession); err != nil {
		return false, "not connected (no session identity)"
	}
	st, _ := statushook.LoadState(dataDir, stateSession)
	if !statushook.ShouldFire(st.LastRun, now, interval) {
		return false, "throttled"
	}
	st.LastRun = now
	if err := st.Save(dataDir, stateSession); err != nil {
		log.Printf("sextant-mcp status: save throttle state: %v", err)
	}
	return true, "fire"
}

// spawnWorker launches the detached worker that does the slow Haiku call, so the
// hook returns immediately and the agent's tool flow is never blocked.
func spawnWorker(session, transcript string) error {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	cmd := exec.Command(self, "status", "--worker", "--session", session, "--transcript", transcript)
	cmd.Env = os.Environ() // carry ANTHROPIC_API_KEY, CLAUDE_PLUGIN_DATA, session env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from the hook's session
	return cmd.Start()                                   // do NOT Wait — fire and forget
}

// runStatusWorker is the detached slow path: read recent activity, ask Haiku for a
// one-line status, and write it to the (stubbed) target. Bounded + fail-soft.
func runStatusWorker(session, transcript string) {
	ctx, cancel := context.WithTimeout(context.Background(), workerBudget)
	defer cancel()

	activity, err := statushook.RecentActivity(transcript, transcriptLines)
	if err != nil || activity == "" {
		log.Printf("sextant-mcp status worker: no activity (%v)", err)
		return
	}
	hc := statushook.HaikuClient{
		APIKey:  os.Getenv(apiKeyEnv),
		Model:   os.Getenv(statusModelEnv),
		BaseURL: os.Getenv(statusAPIBaseEnv), // empty ⇒ the public API; tests/demos point it at a mock
	}
	res, err := hc.Status(ctx, activity)
	if err != nil {
		log.Printf("sextant-mcp status worker: Haiku: %v", err)
		return
	}
	if err := writeStatus(ctx, res); err != nil {
		log.Printf("sextant-mcp status worker: write status (%s): %v", session, err)
	}
}

// writeStatus is the sextant side (TASK-84): the worker connects to the bus as
// this session's identity — following the per-session identity file the MCP
// server wrote, exactly like attest — and upserts its own `status.<self>`
// artifact with the agent.status record. The artifact is a latest-value record,
// single-writer (this agent), so the dash + crew see the agent's current state.
func writeStatus(ctx context.Context, res statushook.StatusResult) error {
	id, err := attest.LoadIdentity(os.Getenv("CLAUDE_PLUGIN_DATA"), os.Getenv(sessionEnv))
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    id.Creds,
		URL:          id.URL,
		ConnInfoPath: filepath.Join(defaultStore(), conninfo.DefaultFile),
		Logf:         log.Printf,
	})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = c.Close() }()

	self := c.ID()
	name := "status." + self
	b, err := json.Marshal(map[string]any{
		"$type":    "agent.status",
		"state":    res.State,
		"headline": res.Headline,
		"updated":  time.Now().UTC().Format(time.RFC3339),
		"by":       self,
	})
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	rec := json.RawMessage(b)
	// Upsert: CAS-update if it exists, else create.
	if art, gerr := c.GetArtifact(ctx, name); gerr == nil {
		_, err = c.UpdateArtifact(ctx, name, rec, art.Revision)
		return err
	}
	_, err = c.CreateArtifact(ctx, name, rec)
	return err
}

// statusInterval is the throttle interval, overridable via SEXTANT_STATUS_INTERVAL
// (seconds) for tuning/tests; defaults to defaultInterval.
func statusInterval() time.Duration {
	if v := os.Getenv(statusIntervalEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultInterval
}
