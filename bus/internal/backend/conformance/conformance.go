// Package conformance is the executable form of the backend contract
// (protocol/semantic-contract.md, ADR-0019): the behaviour every backend module
// must provide. A module's test imports Run and passes a harness factory; the
// suite drives the durable-log and versioned-record primitives and asserts the
// contract. NATS satisfies it today; a future Redis module must pass the same
// suite unchanged — that is what keeps the seam backend-portable.
package conformance

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus/internal/backend"
)

// Harness is what a backend module hands the suite: a ready, empty Backend, a
// log subject root the suite extends (the backend's log must capture
// "<SubjectBase>.>"), and an empty KV bucket the suite may write to.
type Harness struct {
	Backend     backend.Backend
	SubjectBase string
	Bucket      string
}

// Run executes the backend conformance suite. newHarness must return a fresh,
// isolated, empty harness; the suite uses distinct subjects/keys per subtest, so
// one harness per Run is sufficient.
func Run(t *testing.T, newHarness func(t *testing.T) Harness) {
	t.Helper()
	h := newHarness(t)

	t.Run("log: append assigns increasing sequences", func(t *testing.T) {
		ctx := t.Context()
		subj := h.SubjectBase + ".seq"
		s1, err := h.Backend.Append(ctx, subj, []byte(`{"n":1}`))
		mustNil(t, err)
		s2, err := h.Backend.Append(ctx, subj, []byte(`{"n":2}`))
		mustNil(t, err)
		if s2 <= s1 {
			t.Fatalf("sequences not increasing: %d then %d", s1, s2)
		}
	})

	t.Run("log: read from cursor has no gaps and no duplicates", func(t *testing.T) {
		ctx := t.Context()
		subj := h.SubjectBase + ".cursor"
		for i := 0; i < 3; i++ {
			_, err := h.Backend.Append(ctx, subj, []byte(`{"x":1}`))
			mustNil(t, err)
		}
		got, next, err := h.Backend.Read(ctx, subj, 0, 10)
		mustNil(t, err)
		if len(got) != 3 {
			t.Fatalf("read from 0: got %d entries, want 3", len(got))
		}
		// Resuming at next returns nothing new.
		got2, next2, err := h.Backend.Read(ctx, subj, next, 10)
		mustNil(t, err)
		if len(got2) != 0 {
			t.Fatalf("resume at cursor: got %d entries, want 0", len(got2))
		}
		// Appending more and resuming returns only the new ones.
		_, err = h.Backend.Append(ctx, subj, []byte(`{"x":2}`))
		mustNil(t, err)
		got3, _, err := h.Backend.Read(ctx, subj, next2, 10)
		mustNil(t, err)
		if len(got3) != 1 {
			t.Fatalf("resume after append: got %d entries, want 1", len(got3))
		}
	})

	t.Run("log: subscribe delivers new entries", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		subj := h.SubjectBase + ".sub"
		ch, err := h.Backend.Subscribe(ctx, subj, backend.StartNew, 0)
		mustNil(t, err)
		time.Sleep(200 * time.Millisecond) // let the consumer establish
		_, err = h.Backend.Append(ctx, subj, []byte(`{"hi":true}`))
		mustNil(t, err)
		select {
		case e := <-ch:
			if !bytes.Contains(e.Data, []byte(`"hi"`)) {
				t.Fatalf("unexpected entry: %s", e.Data)
			}
			if e.Time.IsZero() {
				t.Error("entry has no substrate timestamp")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("subscribe did not deliver the appended entry")
		}
	})

	t.Run("log: subscribe from a sequence resumes there", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		subj := h.SubjectBase + ".fromseq"
		_, err := h.Backend.Append(ctx, subj, []byte(`{"n":1}`))
		mustNil(t, err)
		s2, err := h.Backend.Append(ctx, subj, []byte(`{"n":2}`))
		mustNil(t, err)
		s3, err := h.Backend.Append(ctx, subj, []byte(`{"n":3}`))
		mustNil(t, err)
		// Resume from the second entry's sequence: it and the third arrive; the
		// first does not (nothing below the resume point, no gap above it).
		ch, err := h.Backend.Subscribe(ctx, subj, backend.StartFromSeq, s2)
		mustNil(t, err)
		for _, want := range []uint64{s2, s3} {
			select {
			case e := <-ch:
				if e.Seq != want {
					t.Fatalf("resumed delivery seq = %d, want %d", e.Seq, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("subscribe from sequence %d did not deliver entry %d", s2, want)
			}
		}
	})

	t.Run("log: subscribe from a gone sequence fails loud", func(t *testing.T) {
		ctx := t.Context()
		subj := h.SubjectBase + ".goneseq"
		s, err := h.Backend.Append(ctx, subj, []byte(`{"n":1}`))
		mustNil(t, err)
		// A resume sequence far beyond the head names history the log has never
		// held (a wiped store, seen at resume time): the backend must refuse with
		// ErrSequenceGone, never wait silently for a sequence that may not come.
		if _, err := h.Backend.Subscribe(ctx, subj, backend.StartFromSeq, s+1000); !errors.Is(err, backend.ErrSequenceGone) {
			t.Fatalf("subscribe far beyond the head returned %v; want backend.ErrSequenceGone", err)
		}
	})

	t.Run("kv: create then get", func(t *testing.T) {
		ctx := t.Context()
		rev, err := h.Backend.Create(ctx, h.Bucket, "a", []byte(`{"v":1}`))
		mustNil(t, err)
		if rev != 1 {
			t.Fatalf("first revision = %d, want 1", rev)
		}
		val, got, err := h.Backend.Get(ctx, h.Bucket, "a")
		mustNil(t, err)
		if got != rev || string(val) != `{"v":1}` {
			t.Fatalf("get = (%s, %d), want (%s, %d)", val, got, `{"v":1}`, rev)
		}
	})

	t.Run("kv: create existing returns ErrKeyExists", func(t *testing.T) {
		ctx := t.Context()
		_, err := h.Backend.Create(ctx, h.Bucket, "dup", []byte(`{}`))
		mustNil(t, err)
		_, err = h.Backend.Create(ctx, h.Bucket, "dup", []byte(`{}`))
		if !errors.Is(err, backend.ErrKeyExists) {
			t.Fatalf("create existing: err = %v, want ErrKeyExists", err)
		}
	})

	t.Run("kv: compare-and-set advances revision; mismatch is rejected", func(t *testing.T) {
		ctx := t.Context()
		rev, err := h.Backend.Create(ctx, h.Bucket, "cas", []byte(`{"v":1}`))
		mustNil(t, err)
		rev2, err := h.Backend.CompareAndSet(ctx, h.Bucket, "cas", []byte(`{"v":2}`), rev)
		mustNil(t, err)
		if rev2 <= rev {
			t.Fatalf("cas revision not advanced: %d then %d", rev, rev2)
		}
		// A stale expected revision is rejected.
		_, err = h.Backend.CompareAndSet(ctx, h.Bucket, "cas", []byte(`{"v":3}`), rev)
		if !errors.Is(err, backend.ErrRevisionMismatch) {
			t.Fatalf("stale cas: err = %v, want ErrRevisionMismatch", err)
		}
	})

	t.Run("kv: get absent returns ErrNotFound", func(t *testing.T) {
		_, _, err := h.Backend.Get(t.Context(), h.Bucket, "nope")
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("get absent: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("kv: put overwrites unconditionally", func(t *testing.T) {
		ctx := t.Context()
		r1, err := h.Backend.Put(ctx, h.Bucket, "put", []byte(`{"v":1}`))
		mustNil(t, err)
		r2, err := h.Backend.Put(ctx, h.Bucket, "put", []byte(`{"v":2}`))
		mustNil(t, err)
		if r2 <= r1 {
			t.Fatalf("put revision not advanced: %d then %d", r1, r2)
		}
		val, _, err := h.Backend.Get(ctx, h.Bucket, "put")
		mustNil(t, err)
		if string(val) != `{"v":2}` {
			t.Fatalf("get after overwrite = %s, want {\"v\":2}", val)
		}
	})

	t.Run("kv: delete removes the key", func(t *testing.T) {
		ctx := t.Context()
		_, err := h.Backend.Create(ctx, h.Bucket, "del", []byte(`{}`))
		mustNil(t, err)
		mustNil(t, h.Backend.Delete(ctx, h.Bucket, "del"))
		_, _, err = h.Backend.Get(ctx, h.Bucket, "del")
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("kv: watch delivers current value then a later write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, err := h.Backend.Create(ctx, h.Bucket, "watch", []byte(`{"v":1}`))
		mustNil(t, err)
		ch, err := h.Backend.Watch(ctx, h.Bucket, "watch")
		mustNil(t, err)
		// Current value first.
		select {
		case c := <-ch:
			if string(c.Value) != `{"v":1}` || c.Deleted {
				t.Fatalf("first change = (%s, deleted=%v), want current value", c.Value, c.Deleted)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watch did not deliver the current value")
		}
		// Then a later write.
		_, err = h.Backend.Put(ctx, h.Bucket, "watch", []byte(`{"v":2}`))
		mustNil(t, err)
		select {
		case c := <-ch:
			if string(c.Value) != `{"v":2}` {
				t.Fatalf("second change = %s, want {\"v\":2}", c.Value)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watch did not deliver the later write")
		}
	})

	t.Run("kv: keys enumerates and empties cleanly", func(t *testing.T) {
		ctx := t.Context()
		// A fresh bucket is provided per harness, but other subtests wrote keys to
		// it; assert our keys are present rather than an exact set.
		_, err := h.Backend.Create(ctx, h.Bucket, "k.one", []byte(`{}`))
		mustNil(t, err)
		_, err = h.Backend.Create(ctx, h.Bucket, "k.two", []byte(`{}`))
		mustNil(t, err)
		keys, err := h.Backend.Keys(ctx, h.Bucket)
		mustNil(t, err)
		if !contains(keys, "k.one") || !contains(keys, "k.two") {
			t.Fatalf("keys missing entries: %v", keys)
		}
	})
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
