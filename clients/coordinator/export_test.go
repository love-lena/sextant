package main

import (
	"context"
	"sync"

	"github.com/love-lena/sextant/sdk/go"
)

// Test-only access to the coordinator internals (export_test.go pattern: an external
// test package gets a controlled keyhole, not a build tag). Used by the AC#3
// content-opacity proof to observe exactly which artifacts a coordinator opens.

// ArtifactReadRecorder installs a hook on every coordinator newCoordinator builds that
// wraps its artifact-read seam and records each name read. It returns the recorder and
// a restore func that clears the hook. Concurrency-safe (coordinators run reads on
// delivery goroutines).
func ArtifactReadRecorder() (*ReadLog, func()) {
	log := &ReadLog{}
	newCoordinatorHook = func(co *coordinator) {
		inner := co.getArtifact
		co.getArtifact = func(ctx context.Context, name string) (sextant.Artifact, error) {
			log.record(name)
			return inner(ctx, name)
		}
	}
	return log, func() { newCoordinatorHook = nil }
}

// ReadLog is the ordered list of artifact names every observed coordinator read.
type ReadLog struct {
	mu    sync.Mutex
	names []string
}

func (l *ReadLog) record(name string) {
	l.mu.Lock()
	l.names = append(l.names, name)
	l.mu.Unlock()
}

// Names returns a snapshot of the recorded reads.
func (l *ReadLog) Names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.names))
	copy(out, l.names)
	return out
}

// Read reports whether name was read.
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
