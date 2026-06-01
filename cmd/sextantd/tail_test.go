package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestTailGenericSubject is the acceptance test for `sextant tail
// <subject>` per slug:feat-bus-tail-cli-verb.
//
// Boots the daemon harness, spawns `sextant tail 'audit.>' --json` as
// a subprocess, publishes a stub audit envelope on `audit.test`, and
// asserts the tail subprocess emits a line containing the envelope's
// id within 5s. JSON mode is the easiest assertion target — text mode
// renders a one-line summary but does not include the envelope id.
func TestTailGenericSubject(t *testing.T) {
	h := startDaemonHarness(t)
	// rpcClient also waits for the RPC surface to be up; we don't need
	// the RPC verbs themselves but readiness implies NATS + JetStream
	// streams are wired (the audit stream is created during boot).
	cli := rpcClient(t, h)

	sextantBin := buildSextantBinary(t)
	configDir := h.cfg.Paths.ConfigDir

	// Launch `sextant tail audit.> --json` as a subprocess. The CLI
	// streams forever, so we tie it to a context we cancel at the end
	// of the test. Buffer stdout into a thread-safe accumulator the
	// assertion loop polls.
	tailCtx, tailCancel := context.WithCancel(context.Background())
	defer tailCancel()
	tailCmd := exec.CommandContext(tailCtx, sextantBin, //nolint:gosec // test-controlled args
		"tail", "audit.>",
		"--config-dir", configDir,
		"--json")
	var (
		mu  sync.Mutex
		buf bytes.Buffer
		se  bytes.Buffer
	)
	tailCmd.Stdout = &lockedWriter{mu: &mu, w: &buf}
	tailCmd.Stderr = &se
	if err := tailCmd.Start(); err != nil {
		t.Fatalf("start sextant tail: %v", err)
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- tailCmd.Wait() }()
	t.Cleanup(func() {
		tailCancel()
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
			_ = tailCmd.Process.Kill()
			<-doneCh
		}
	})

	// Publish a stub audit envelope on audit.test. The tail subscriber
	// uses an ordered JetStream consumer with DeliverNew, so we must
	// give the subscription a moment to bind before publishing — poll
	// for the subprocess to be alive on its own + wait briefly.
	// 200ms is enough on the test harness; bump to 1s for slow CI.
	if err := waitForTailSubscribed(tailCmd, doneCh, 2*time.Second); err != nil {
		t.Fatalf("tail subprocess did not stabilize: %v\nstderr=%s", err, se.String())
	}

	stub, err := sextantproto.NewEnvelopeWith(sextantproto.KindAudit,
		sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "operator"},
		sextantproto.AuditPayload{
			Actor:  "operator",
			Action: "test.tail_generic_subject",
			Result: sextantproto.AuditAllowed,
		})
	if err != nil {
		t.Fatalf("build stub envelope: %v", err)
	}
	// Publish via JetStream so the message lands in the audit stream
	// the tail's ordered consumer is reading from. The pkg/client
	// Publish path is core NATS, which won't reach the JS stream
	// directly — use the underlying JetStream context.
	raw, err := json.Marshal(stub)
	if err != nil {
		t.Fatalf("marshal stub: %v", err)
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if _, err := cli.JetStream().Publish(pubCtx, "audit.test.tail", raw); err != nil {
		t.Fatalf("JS Publish audit.test.tail: %v", err)
	}

	// Poll the tail subprocess's stdout for a line containing the
	// stub envelope's id. 5s upper bound per acceptance text.
	want := stub.ID.String()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Contains(got, want) {
			// Sanity-decode the matching line as an envelope to make
			// sure we're really seeing the tail's JSON output, not
			// some accidental occurrence of the UUID elsewhere.
			for _, line := range strings.Split(got, "\n") {
				if !strings.Contains(line, want) {
					continue
				}
				var env sextantproto.Envelope
				if err := json.Unmarshal([]byte(line), &env); err != nil {
					t.Fatalf("tail output line is not envelope JSON: %v (%q)", err, line)
				}
				if env.ID != stub.ID {
					t.Fatalf("envelope id mismatch: got %s, want %s", env.ID, stub.ID)
				}
				return
			}
			t.Fatalf("matched id substring but no line parsed as Envelope: %q", got)
		}
		if time.Now().After(deadline) {
			t.Fatalf("tail did not emit envelope id %s within 5s\nstdout=%q\nstderr=%q",
				want, got, se.String())
		}
		select {
		case err := <-doneCh:
			t.Fatalf("tail subprocess exited before we observed the envelope: err=%v\nstdout=%q\nstderr=%q",
				err, got, se.String())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitForTailSubscribed gives the tail subprocess a brief window to
// boot, connect to NATS, and bind its ordered consumer. We can't
// observe binding directly (the consumer is server-side), so we just
// sleep for the supplied window and bail early if the process exited.
func waitForTailSubscribed(cmd *exec.Cmd, doneCh <-chan error, window time.Duration) error {
	select {
	case err := <-doneCh:
		if err == nil {
			return errors.New("tail subprocess exited 0 before publish")
		}
		return err
	case <-time.After(window):
		_ = cmd // keep the parameter even though we only used it for tying lifetime
		return nil
	}
}

// lockedWriter wraps an io.Writer with a mutex so the subprocess
// stdout pump can race the assertion loop's reader safely.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
