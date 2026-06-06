//go:build e2e

// Package e2e is the M2 definition-of-done: it drives the built `sextant` binary
// through the collaboration loop in tests/e2e/m2-acceptance.md — enrollment (both
// auth modes), a message with an unforgeable author, a shared artifact via
// compare-and-set, the live directory with presence, durable identity across
// reconnect, and retire — and checks both the per-step asserts (the teeth) and the
// normalized transcript against a golden (regenerate with `-update`).
//
// It is behind the `e2e` build tag (and tests/e2e/run.sh) so it is runnable on
// demand and stays out of the default unit-test gate until it is wired into CI as
// the M2 DoD e2e.
package e2e

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

var update = flag.Bool("update", false, "regenerate the golden transcript")

const (
	topic       = "msg.topic.plan"
	artifact    = "the-plan"
	stepTimeout = 15 * time.Second
)

func TestM2Acceptance(t *testing.T) {
	h := newHarness(t)
	h.startBus()

	// --- 0: bus up -------------------------------------------------------------
	h.rec("0 — bus up", h.busBanner)
	if _, err := os.Stat(filepath.Join(h.store, "bus.json")); err != nil {
		t.Fatalf("discovery file not written: %v", err)
	}

	// --- 1: alice is issued by the operator; bob self-enrolls ------------------
	aliceOut, code := h.run(nil, "clients", "register", "alice", "--kind", "worker", "--store", h.store)
	if code != 0 {
		t.Fatalf("register alice exited %d: %s", code, aliceOut)
	}
	bobOut, code := h.run(map[string]string{"USER": "bob"}, "clients", "register", "--self", "--kind", "reviewer", "--store", h.store)
	if code != 0 {
		t.Fatalf("register --self exited %d: %s", code, bobOut)
	}
	aliceID := mustParseID(t, aliceOut, `registered alice as (`+ulidPat+`)`)
	bobID := mustParseID(t, bobOut, `enrolled as (`+ulidPat+`)`)
	if aliceID == bobID {
		t.Fatalf("alice and bob got the same id %q (must be distinct bus-minted ULIDs)", aliceID)
	}
	h.label(aliceID, "alice")
	h.label(bobID, "bob")
	aliceCreds := filepath.Join(h.store, "alice.creds")
	bobCreds := filepath.Join(h.store, "bob.creds")
	for _, p := range []string{aliceCreds, bobCreds} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("creds not written: %s: %v", p, err)
		}
	}
	h.rec("1 — issuance (operator mints alice; bob self-enrolls)", aliceOut+bobOut)

	// --- 2: bob subscribes; alice publishes; unforgeable author ----------------
	sub := h.startBg(map[string]string{}, "subscribe", topic, "--creds", bobCreds, "--store", h.store)
	sub.waitStderr(t, "subscribed to "+topic)
	pubOut, code := h.run(nil, "publish", topic, `{"hello":"world"}`, "--creds", aliceCreds, "--store", h.store)
	if code != 0 {
		t.Fatalf("publish exited %d: %s", code, pubOut)
	}
	delivery := sub.waitStdout(t, "["+topic+"]")
	// Keystone: the frame bob receives is authored by alice's bus-minted id.
	if !strings.Contains(delivery, aliceID) {
		t.Fatalf("delivered frame author is not alice's id %q: %q", aliceID, delivery)
	}
	if strings.Contains(delivery, bobID) {
		t.Fatalf("delivered frame must not carry bob's id as author: %q", delivery)
	}
	// And alice cannot forge a frame authored by bob: a raw publish under bob's
	// call prefix, using alice's own credential, is denied by the allow-list.
	h.assertCannotForge(t, aliceCreds, aliceID, bobID)
	h.rec("2 — message with an unforgeable author", pubOut+normalizeDelivery(delivery))

	// --- 3: a shared artifact, via compare-and-set -----------------------------
	createOut, code := h.run(nil, "artifact", "create", artifact, `{"title":"v1"}`, "--creds", aliceCreds, "--store", h.store)
	if code != 0 || !strings.Contains(createOut, "revision 1") {
		t.Fatalf("artifact create: code=%d out=%q", code, createOut)
	}
	updOut, code := h.run(nil, "artifact", "update", artifact, `{"title":"v2"}`, "--rev", "1", "--creds", bobCreds, "--store", h.store)
	if code != 0 || !strings.Contains(updOut, "revision 2") {
		t.Fatalf("artifact update by bob: code=%d out=%q", code, updOut)
	}
	staleOut, code := h.run(nil, "artifact", "update", artifact, `{"title":"v3"}`, "--rev", "1", "--creds", aliceCreds, "--store", h.store)
	if code == 0 {
		t.Fatalf("stale CAS update should fail (non-zero exit); got code=0 out=%q", staleOut)
	}
	getOut, code := h.run(nil, "artifact", "get", artifact, "--creds", aliceCreds, "--store", h.store)
	if code != 0 || !strings.Contains(getOut, "revision 2") || !strings.Contains(getOut, `{"title":"v2"}`) {
		t.Fatalf("artifact get: code=%d out=%q", code, getOut)
	}
	h.rec("3 — shared artifact via compare-and-set", createOut+updOut+staleOut+getOut)

	// --- 4: the live directory shows presence ----------------------------------
	h.waitPresence(t, aliceCreds, bobID, true)
	listOut := h.listClients(t, aliceCreds)
	assertPresence(t, listOut, aliceID, "online")
	assertPresence(t, listOut, bobID, "online")
	h.rec("4 — the live directory shows presence", listOut)

	// --- 5: durable identity across disconnect/reconnect -----------------------
	sub.stop() // clean SIGINT: bob disconnects
	h.waitPresence(t, aliceCreds, bobID, false)
	offlineList := h.listClients(t, aliceCreds)
	assertPresence(t, offlineList, bobID, "offline") // still listed — durable, not reaped
	assertPresence(t, offlineList, aliceID, "online")

	sub2 := h.startBg(map[string]string{}, "subscribe", topic, "--creds", bobCreds, "--store", h.store)
	sub2.waitStderr(t, "subscribed to "+topic)
	h.waitPresence(t, aliceCreds, bobID, true)
	reconnList := h.listClients(t, aliceCreds)
	assertPresence(t, reconnList, bobID, "online") // SAME bobID flipped back online
	h.rec("5 — durable identity across reconnect (offline, then back online)", offlineList+reconnList)

	// --- 6: retire decommissions the identity ----------------------------------
	retireOut, code := h.run(nil, "clients", "retire", bobID, "--store", h.store)
	if code != 0 || !strings.Contains(retireOut, "retired "+bobID) {
		t.Fatalf("retire bob: code=%d out=%q", code, retireOut)
	}
	sub2.stop()
	finalList := h.listClients(t, aliceCreds)
	if strings.Contains(finalList, bobID) {
		t.Fatalf("retired identity %q must be gone from the directory: %q", bobID, finalList)
	}
	assertPresence(t, finalList, aliceID, "online")
	h.rec("6 — retire decommissions the identity", retireOut+finalList)

	h.compareGolden(t)
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

const ulidPat = `[0-9A-HJKMNP-TV-Z]{26}`

var (
	ulidRe = regexp.MustCompile(ulidPat)
	tsRe   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)
	urlRe  = regexp.MustCompile(`nats://[0-9.]+:\d+`)
	wsRe   = regexp.MustCompile(`[ \t]{2,}`)
)

type harness struct {
	t         *testing.T
	bin       string
	store     string
	busCmd    *exec.Cmd
	busBanner string
	labels    map[string]string // id -> token (alice/bob)
	mu        sync.Mutex
	steps     []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	bin := buildBinary(t)
	return &harness{t: t, bin: bin, store: t.TempDir(), labels: map[string]string{}}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sextant")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/sextant")
	cmd.Dir = "../.." // repo root, relative to tests/e2e
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sextant: %v\n%s", err, out)
	}
	return bin
}

func (h *harness) startBus() {
	h.t.Helper()
	var stdout bgBuffer
	cmd := exec.Command(h.bin, "up", "--store", h.store, "--port", "0")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start bus: %v", err)
	}
	h.busCmd = cmd
	h.t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	// Wait for the discovery file (the bus is reachable only once it is written).
	deadline := time.Now().Add(stepTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(h.store, "bus.json")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(h.store, "bus.json")); err != nil {
		h.t.Fatalf("bus did not come up within %s:\n%s", stepTimeout, stdout.String())
	}
	// Give the banner a beat to flush, then capture it.
	time.Sleep(100 * time.Millisecond)
	h.busBanner = stdout.String()
}

// run executes the binary to completion and returns combined stdout+stderr and
// the exit code. Extra env entries (k=v) override the inherited environment.
func (h *harness) run(env map[string]string, args ...string) (string, int) {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err == nil {
		return buf.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return buf.String(), ee.ExitCode()
	}
	h.t.Fatalf("run %v: %v", args, err)
	return "", -1
}

// listClients runs `clients list` as the given creds and returns its output.
func (h *harness) listClients(t *testing.T, creds string) string {
	t.Helper()
	out, code := h.run(nil, "clients", "list", "--creds", creds, "--store", h.store)
	if code != 0 {
		t.Fatalf("clients list exited %d: %s", code, out)
	}
	return out
}

// waitPresence polls the directory until id reaches the wanted online state.
func (h *harness) waitPresence(t *testing.T, creds, id string, online bool) {
	t.Helper()
	want := "offline"
	if online {
		want = "online"
	}
	deadline := time.Now().Add(stepTimeout)
	var last string
	for time.Now().Before(deadline) {
		last = h.listClients(t, creds)
		if lineFor(last, id) == "" && !online {
			// not present yet is not what we want for offline; keep waiting only if
			// the record should exist (it always does here)
		}
		if presenceOf(last, id) == want {
			return
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatalf("client %q did not reach %s within %s:\n%s", id, want, stepTimeout, last)
}

// assertCannotForge proves the unforgeable-author guarantee: alice, using her own
// credential, cannot publish under bob's call prefix — the allow-list denies it.
func (h *harness) assertCannotForge(t *testing.T, aliceCreds, aliceID, bobID string) {
	t.Helper()
	nc, err := nats.Connect(busURL(t, h.store),
		nats.UserCredentials(aliceCreds),
		nats.CustomInboxPrefix("_INBOX."+aliceID))
	if err != nil {
		t.Fatalf("forge probe connect: %v", err)
	}
	defer nc.Close()
	errCh := make(chan error, 4)
	nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		select {
		case errCh <- e:
		default:
		}
	})
	// Publish under bob's API prefix (sx.api.<bobID>.message.publish): outside
	// alice's allow-list (sx.api.<aliceID>.>), so the server rejects it.
	if err := nc.Publish("sx.api."+bobID+".message.publish", []byte(`{"subject":"`+topic+`","record":{"forged":true}}`)); err != nil {
		t.Fatalf("forge probe publish: %v", err)
	}
	_ = nc.Flush()
	select {
	case e := <-errCh:
		if !strings.Contains(e.Error(), "Permissions Violation") {
			t.Fatalf("expected a permissions violation forging bob's author, got: %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forging a frame under bob's id was not denied (no permissions violation)")
	}
}

// rec records a normalized step in the transcript. It normalizes BEFORE taking
// the lock — normalize locks h.mu itself to read the labels, and sync.Mutex is
// not reentrant, so holding the lock across the normalize call would deadlock.
func (h *harness) rec(title, body string) {
	entry := "### " + title + "\n" + h.normalize(body)
	h.mu.Lock()
	h.steps = append(h.steps, entry)
	h.mu.Unlock()
}

func (h *harness) label(id, token string) {
	h.mu.Lock()
	h.labels[id] = token
	h.mu.Unlock()
}

// normalize masks volatile values so the transcript is deterministic: known ids
// to <ULID:alice>/<ULID:bob>, any other ULID to <ULID>, timestamps to <TS>, the
// bus URL to <URL>, the store path to <PATH>, and runs of spaces collapsed (so
// column widths that shift under id substitution do not matter).
func (h *harness) normalize(s string) string {
	s = strings.ReplaceAll(s, h.store, "<PATH>")
	s = urlRe.ReplaceAllString(s, "<URL>")
	h.mu.Lock()
	for id, tok := range h.labels {
		s = strings.ReplaceAll(s, id, "<ULID:"+tok+">")
	}
	h.mu.Unlock()
	s = ulidRe.ReplaceAllString(s, "<ULID>")
	s = tsRe.ReplaceAllString(s, "<TS>")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(wsRe.ReplaceAllString(ln, " "), " ")
	}
	return strings.Join(lines, "\n")
}

func (h *harness) compareGolden(t *testing.T) {
	t.Helper()
	got := strings.Join(h.steps, "\n") + "\n"
	golden := filepath.Join("testdata", "m2-acceptance.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote golden %s", golden)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("transcript does not match golden (run with -update to regenerate)\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// ---------------------------------------------------------------------------
// background processes
// ---------------------------------------------------------------------------

type bgProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout *bgBuffer
	stderr *bgBuffer
	once   sync.Once
}

func (h *harness) startBg(env map[string]string, args ...string) *bgProc {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, errb := &bgBuffer{}, &bgBuffer{}
	cmd.Stdout = out
	cmd.Stderr = errb
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start %v: %v", args, err)
	}
	p := &bgProc{t: h.t, cmd: cmd, stdout: out, stderr: errb}
	h.t.Cleanup(p.stop)
	return p
}

func (p *bgProc) stop() {
	p.once.Do(func() {
		_ = p.cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
		}
	})
}

func (p *bgProc) waitStderr(t *testing.T, substr string) string { return waitBuf(t, p.stderr, substr) }

func (p *bgProc) waitStdout(t *testing.T, substr string) string { return waitBuf(t, p.stdout, substr) }

func waitBuf(t *testing.T, b *bgBuffer, substr string) string {
	t.Helper()
	deadline := time.Now().Add(stepTimeout)
	for time.Now().Before(deadline) {
		if ln := b.lineContaining(substr); ln != "" {
			return ln
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("did not see %q within %s; buffer:\n%s", substr, stepTimeout, b.String())
	return ""
}

// bgBuffer is a goroutine-safe buffer for a child's stdout/stderr.
type bgBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *bgBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *bgBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *bgBuffer) lineContaining(substr string) string {
	for _, ln := range strings.Split(b.String(), "\n") {
		if strings.Contains(ln, substr) {
			return ln
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustParseID(t *testing.T, out, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(out)
	if len(m) < 2 {
		t.Fatalf("could not parse id from %q with %q", out, pattern)
	}
	return m[1]
}

// lineFor returns the directory line for id, or "".
func lineFor(list, id string) string {
	for _, ln := range strings.Split(list, "\n") {
		if strings.Contains(ln, id) {
			return ln
		}
	}
	return ""
}

// presenceOf returns "online"/"offline" for id in a clients-list output, or "".
func presenceOf(list, id string) string {
	ln := lineFor(list, id)
	switch {
	case ln == "":
		return ""
	case strings.Contains(ln, "offline"):
		return "offline"
	case strings.Contains(ln, "online"):
		return "online"
	default:
		return ""
	}
}

func assertPresence(t *testing.T, list, id, want string) {
	t.Helper()
	if got := presenceOf(list, id); got != want {
		t.Fatalf("client %q presence = %q, want %q\n%s", id, got, want, list)
	}
}

// normalizeDelivery keeps only the delivery line for the transcript.
func normalizeDelivery(line string) string {
	return strings.TrimRight(line, "\n") + "\n"
}

func busURL(t *testing.T, store string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(store, "bus.json"))
	if err != nil {
		t.Fatalf("read bus.json: %v", err)
	}
	m := urlRe.FindString(string(b))
	if m == "" {
		t.Fatalf("no url in bus.json: %s", b)
	}
	return m
}
