package main

import (
	"context"
	"sync"
	"time"

	"github.com/love-lena/sextant/sdk/go"
)

// SetTerminalGraceHook installs a hook that pins every coordinator's terminalGrace — the
// window the run-topic subscription stays alive past a terminal run so a late steer is
// reported not-applied (TASK-246). The proof test sets a SHORT grace so it can assert the
// not-applied notice fires without waiting the production default. Returns a restore func
// that clears the hook. Composes with any hook already set (e.g. ArtifactReadRecorder).
func SetTerminalGraceHook(d time.Duration) func() {
	prev := newCoordinatorHook
	newCoordinatorHook = func(co *coordinator) {
		if prev != nil {
			prev(co)
		}
		co.terminalGrace = d
	}
	return func() { newCoordinatorHook = prev }
}

// SetOpenPRHook installs a hook that replaces every coordinator's openPR seam (TASK-260)
// — the trusted-path PR-open's git/gh path — with fn. The PR-open proof uses it to drive a
// run's pr-open step against a LOCAL bare repo (a real branch commit + push) while stubbing
// only the gh `pr create` call (the LIVE half), so the run's PR-open path is exercised
// end-to-end without a real GitHub. Returns a restore func; composes with any prior hook.
func SetOpenPRHook(fn openPRFunc) func() {
	prev := newCoordinatorHook
	newCoordinatorHook = func(co *coordinator) {
		if prev != nil {
			prev(co)
		}
		co.openPR = fn
	}
	return func() { newCoordinatorHook = prev }
}

// Test-only access to the coordinator internals (export_test.go pattern: an external
// test package gets a controlled keyhole, not a build tag). Used by the AC#3
// content-opacity proof to observe exactly which artifacts a coordinator opens.

// ArtifactReadRecorder installs a hook on every coordinator newCoordinator builds that
// wraps BOTH artifact seams and records, separately, each name the coordinator (a) reads
// the CONTENT of (getArtifact) and (b) merely PROBES for existence (existsArtifact). The
// split lets the content-opacity proof (AC#3) assert the coordinator never reads a work-
// step artifact's CONTENT, while allowing — and observing — the proof gate's existence
// probes (TASK-243), which discard the body and are metadata, not a content read. Returns
// the recorder and a restore func that clears the hook. Concurrency-safe (coordinators
// run reads on delivery goroutines).
func ArtifactReadRecorder() (*ReadLog, func()) {
	log := &ReadLog{}
	newCoordinatorHook = func(co *coordinator) {
		innerGet := co.getArtifact
		co.getArtifact = func(ctx context.Context, name string) (sextant.Artifact, error) {
			log.record(name)
			return innerGet(ctx, name)
		}
		innerExists := co.existsArtifact
		co.existsArtifact = func(ctx context.Context, name string) error {
			log.recordExists(name)
			return innerExists(ctx, name)
		}
	}
	return log, func() { newCoordinatorHook = nil }
}

// ReadLog records, separately, the artifact names the coordinator read the CONTENT of
// (names) and the names it merely PROBED for existence (exists). Content reads are what
// the content-opacity proof (AC#3) constrains; existence probes are metadata and tracked
// apart so a test can tell them apart.
type ReadLog struct {
	mu     sync.Mutex
	names  []string
	exists []string
}

func (l *ReadLog) record(name string) {
	l.mu.Lock()
	l.names = append(l.names, name)
	l.mu.Unlock()
}

func (l *ReadLog) recordExists(name string) {
	l.mu.Lock()
	l.exists = append(l.exists, name)
	l.mu.Unlock()
}

// Names returns a snapshot of the recorded CONTENT reads.
func (l *ReadLog) Names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.names))
	copy(out, l.names)
	return out
}

// Read reports whether name's CONTENT was read (getArtifact). It is false for an
// existence probe, which never opens the body.
func (l *ReadLog) Read(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, n := range l.names {
		if n == name {
			return true
		}
	}
	return false
}

// ExistsProbed reports whether name was existence-probed (existsArtifact).
func (l *ReadLog) ExistsProbed(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, n := range l.exists {
		if n == name {
			return true
		}
	}
	return false
}
