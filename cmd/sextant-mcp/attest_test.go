package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/love-lena/sextant/internal/attest"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
)

// attestFixture stands up an in-process bus, a worker whose DM the hook scans,
// and a principal publisher whose DM lands on the worker's subject. The hook now
// FOLLOWS the server's identity via the per-session identity file (it no longer
// re-resolves), so the fixture SEEDS that file with the worker's creds + URL via
// attest.SaveIdentity — the exact call the MCP server makes on connect. cf
// carries only the store/url for discovery; identity comes from the file.
type attestFixture struct {
	cf        connFlags
	dataDir   string
	sessionID string
	cursorPth string
	workerID  string // the worker's ULID; its DM subject is sx.ClientSubject(workerID)
}

func newAttestFixture(t *testing.T) attestFixture {
	t.Helper()
	store := t.TempDir()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	url := b.ClientURL()

	// The worker: the hook connects as this identity and scans its own DM.
	workerCreds, workerID, err := b.MintClient(t.Context(), "worker", "worker")
	if err != nil {
		t.Fatalf("MintClient(worker): %v", err)
	}
	workerCredsPath := filepath.Join(t.TempDir(), "worker.creds")
	if err := os.WriteFile(workerCredsPath, []byte(workerCreds), 0o600); err != nil {
		t.Fatal(err)
	}

	// The principal: designate it, then DM the worker as it.
	princCreds, princID, err := b.MintClient(t.Context(), "human", "human")
	if err != nil {
		t.Fatalf("MintClient(human): %v", err)
	}
	princCredsPath := filepath.Join(t.TempDir(), "human.creds")
	if err := os.WriteFile(princCredsPath, []byte(princCreds), 0o600); err != nil {
		t.Fatal(err)
	}

	iss, err := sextant.ConnectIssuer(t.Context(), sextant.Options{
		URL:       url,
		CredsPath: bus.OperatorCredsPath(store),
	})
	if err != nil {
		t.Fatalf("ConnectIssuer (operator): %v", err)
	}
	defer func() { _ = iss.Close() }()
	if err := iss.SetPrincipal(t.Context(), princID); err != nil {
		t.Fatalf("SetPrincipal: %v", err)
	}

	// The principal DMs the worker (the trust path: a principal DM on the
	// worker's own subject).
	princ, err := sextant.Connect(t.Context(), sextant.Options{URL: url, CredsPath: princCredsPath})
	if err != nil {
		t.Fatalf("Connect(principal): %v", err)
	}
	defer func() { _ = princ.Close() }()
	dm := sx.ClientSubject(workerID)
	if err := princ.Publish(t.Context(), dm, json.RawMessage(`{"$type":"chat.message","text":"ship the v0.2 release"}`)); err != nil {
		t.Fatalf("principal publish DM: %v", err)
	}

	dataDir := t.TempDir()
	sessionID := "attest-unit-session"

	// Seed the per-session identity file the hook follows — exactly what the MCP
	// server writes on connect. The hook reads {creds, url} from here and connects
	// as the worker, so it scans the worker's own DM (where the principal's DM landed).
	if err := attest.SaveIdentity(dataDir, sessionID, attest.Identity{
		Creds: workerCredsPath,
		URL:   url,
		ID:    workerID,
	}); err != nil {
		t.Fatalf("seed identity file: %v", err)
	}

	emptyCreds := ""
	emptyCtx := ""
	storeCp := store
	urlCp := url
	return attestFixture{
		cf:        connFlags{creds: &emptyCreds, store: &storeCp, url: &urlCp, context: &emptyCtx},
		dataDir:   dataDir,
		sessionID: sessionID,
		cursorPth: filepath.Join(dataDir, "attest-cursor", sessionID+".json"),
		workerID:  workerID,
	}
}

// errEmit is a sentinel an emit callback returns to simulate a failed stdout
// write/flush on the trust path.
var errEmit = errors.New("emit boom")

// TestAttestCursorNotAdvancedOnEmitFailure proves M2 (review): when the emit of
// the trusted block fails, attestOnce returns the error AND does NOT advance the
// cursor — so a subsequent successful run re-delivers the same block (re-delivery
// beats a silent at-most-once loss on the operator-equivalent path).
func TestAttestCursorNotAdvancedOnEmitFailure(t *testing.T) {
	f := newAttestFixture(t)
	t.Setenv("CLAUDE_PLUGIN_DATA", f.dataDir)

	// First run: emit FAILS. The block must have been built (there is a DM), but
	// the cursor must not advance.
	failEmit := func(string) error { return errEmit }
	err := attestOnce(t.Context(), f.cf, f.sessionID, failEmit)
	if !errors.Is(err, errEmit) {
		t.Fatalf("attestOnce with a failing emit: err = %v, want errEmit", err)
	}
	assertCursorNotAdvanced(t, f)

	// Second run: emit SUCCEEDS. Because the cursor never advanced, the principal
	// DM re-delivers — it was NOT silently lost.
	var got string
	okEmit := func(out string) error { got = out; return nil }
	if err := attestOnce(t.Context(), f.cf, f.sessionID, okEmit); err != nil {
		t.Fatalf("attestOnce (recovery run): %v", err)
	}
	block := mustParseBlock(t, got)
	if !strings.Contains(block, "ship the v0.2 release") || !strings.Contains(block, "PRINCIPAL") {
		t.Fatalf("recovery run did not re-deliver the principal DM:\n%s", block)
	}
	assertCursorAdvanced(t, f)
}

// TestAttestAdvancesAfterSuccessfulEmit proves the success ordering: a single
// successful run delivers the block AND advances the cursor, and a second run in
// the same session then delivers nothing (at-most-once on a successful emit).
func TestAttestAdvancesAfterSuccessfulEmit(t *testing.T) {
	f := newAttestFixture(t)
	t.Setenv("CLAUDE_PLUGIN_DATA", f.dataDir)

	var first string
	emitN := 0
	okEmit := func(out string) error { emitN++; first = out; return nil }
	if err := attestOnce(t.Context(), f.cf, f.sessionID, okEmit); err != nil {
		t.Fatalf("attestOnce (first): %v", err)
	}
	if emitN != 1 {
		t.Fatalf("first run emitted %d blocks, want 1", emitN)
	}
	block := mustParseBlock(t, first)
	if !strings.Contains(block, "ship the v0.2 release") {
		t.Fatalf("first run missing the principal DM:\n%s", block)
	}
	assertCursorAdvanced(t, f)

	// Second run, same session: nothing new — emit is NEVER called.
	called := false
	if err := attestOnce(t.Context(), f.cf, f.sessionID, func(string) error { called = true; return nil }); err != nil {
		t.Fatalf("attestOnce (second): %v", err)
	}
	if called {
		t.Fatal("second run emitted a block; the cursor failed to suppress an already-delivered DM (at-most-once violated)")
	}
}

func assertCursorAdvanced(t *testing.T, f attestFixture) {
	t.Helper()
	if _, err := os.Stat(f.cursorPth); err != nil {
		t.Fatalf("expected an advanced cursor file at %s: %v", f.cursorPth, err)
	}
	cur, err := attest.LoadCursor(f.dataDir, f.sessionID)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	// The DM is the only frame on the subject; an advanced cursor is non-zero.
	if next := cur.Since(sx.ClientSubject(f.workerID)); next == 0 {
		t.Fatalf("cursor not advanced for the worker's DM subject (still 0)")
	}
}

func assertCursorNotAdvanced(t *testing.T, f attestFixture) {
	t.Helper()
	// Either no cursor file (never saved) or a zero cursor: both prove "not
	// advanced". A non-zero saved cursor is the failure.
	cur, err := attest.LoadCursor(f.dataDir, f.sessionID)
	if err != nil {
		t.Fatalf("LoadCursor: %v", err)
	}
	if next := cur.Since(sx.ClientSubject(f.workerID)); next != 0 {
		t.Fatalf("cursor advanced to %d despite a failed emit; the block could be silently lost", next)
	}
}

func mustParseBlock(t *testing.T, out string) string {
	t.Helper()
	var ho attest.HookOutput
	if err := json.Unmarshal([]byte(out), &ho); err != nil {
		t.Fatalf("emit output is not hookSpecificOutput JSON: %v\n%s", err, out)
	}
	return ho.HookSpecificOutput.AdditionalContext
}
