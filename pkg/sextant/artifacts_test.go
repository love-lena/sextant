package sextant

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestCreateGetArtifact(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-create")
	ctx := t.Context()

	rev, err := c.CreateArtifact(ctx, "plan/a", []byte(`{"v":1}`))
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	a, err := c.GetArtifact(ctx, "plan/a")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if !bytes.Equal(a.Record, []byte(`{"v":1}`)) {
		t.Errorf(`record = %s, want {"v":1}`, a.Record)
	}
	if a.Revision != rev {
		t.Errorf("revision = %d, want %d", a.Revision, rev)
	}
	if a.Name != "plan/a" {
		t.Errorf("name = %q", a.Name)
	}
}

func TestCreateRejectsNonLexicon(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-badval")
	ctx := t.Context()
	if _, err := c.CreateArtifact(ctx, "bad", []byte("not json")); err == nil {
		t.Error("expected Create to reject a non-JSON value")
	}
	if _, err := c.CreateArtifact(ctx, "empty", nil); err == nil {
		t.Error("expected Create to reject an empty value")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-dup")
	ctx := t.Context()
	if _, err := c.CreateArtifact(ctx, "dup", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateArtifact(ctx, "dup", []byte(`{"a":2}`)); err == nil {
		t.Error("expected Create to reject an existing artifact")
	}
}

func TestUpdateCAS(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-cas")
	ctx := t.Context()

	rev1, err := c.CreateArtifact(ctx, "doc", []byte(`{"r":1}`))
	if err != nil {
		t.Fatal(err)
	}
	rev2, err := c.UpdateArtifact(ctx, "doc", []byte(`{"r":2}`), rev1)
	if err != nil {
		t.Fatalf("CAS update with current rev should succeed: %v", err)
	}
	if rev2 <= rev1 {
		t.Errorf("revision did not advance: %d -> %d", rev1, rev2)
	}
	// A stale update (using the old revision) must conflict.
	if _, err := c.UpdateArtifact(ctx, "doc", []byte(`{"r":3}`), rev1); err == nil {
		t.Error("expected a CAS conflict on a stale revision")
	}
}

func TestDeleteArtifact(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-del")
	ctx := t.Context()
	if _, err := c.CreateArtifact(ctx, "tmp", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteArtifact(ctx, "tmp"); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	if _, err := c.GetArtifact(ctx, "tmp"); err == nil {
		t.Error("expected Get to fail after delete")
	}
}

func TestWatchArtifact(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-watch")
	ctx := t.Context()

	got := make(chan ArtifactChange, 8)
	w, err := c.WatchArtifact(ctx, "watched", func(ch ArtifactChange) { got <- ch })
	if err != nil {
		t.Fatalf("WatchArtifact: %v", err)
	}
	defer func() { _ = w.Stop() }()

	if _, err := c.CreateArtifact(ctx, "watched", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-got:
		if ch.Deleted {
			t.Error("a create should not arrive as a delete")
		}
		if !bytes.Equal(ch.Record, []byte(`{"v":1}`)) {
			t.Errorf(`watched record = %s, want {"v":1}`, ch.Record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not deliver the create")
	}
}

// TestWatchArtifactExistingThenDelete covers the two paths the live-create test
// misses: delivery of an already-present value when the watch starts, and a
// delete arriving with Deleted set (not as an empty-record value).
func TestWatchArtifactExistingThenDelete(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-watch2")
	ctx := t.Context()

	if _, err := c.CreateArtifact(ctx, "doc", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	got := make(chan ArtifactChange, 8)
	w, err := c.WatchArtifact(ctx, "doc", func(ch ArtifactChange) { got <- ch })
	if err != nil {
		t.Fatalf("WatchArtifact: %v", err)
	}
	defer func() { _ = w.Stop() }()

	// First delivery is the existing value.
	select {
	case ch := <-got:
		if ch.Deleted || !bytes.Equal(ch.Record, []byte(`{"v":1}`)) {
			t.Errorf("first delivery = %+v, want the existing value", ch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not deliver the existing value")
	}

	// A delete arrives explicitly flagged.
	if err := c.DeleteArtifact(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-got:
		if !ch.Deleted {
			t.Errorf("delete should arrive with Deleted=true, got %+v", ch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not deliver the delete")
	}
}

// TestWatchArtifactStopsOnContextCancel verifies the watch tears down when the
// caller cancels the context it was created with, not only on Stop.
func TestWatchArtifactStopsOnContextCancel(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "art-watch-cancel")

	subCtx, cancel := context.WithCancel(t.Context())
	got := make(chan ArtifactChange, 4)
	w, err := c.WatchArtifact(subCtx, "wc", func(ch ArtifactChange) { got <- ch })
	if err != nil {
		t.Fatalf("WatchArtifact: %v", err)
	}
	defer func() { _ = w.Stop() }()

	cancel()                           // cancelling ctx should wind the watch down
	time.Sleep(300 * time.Millisecond) // let the bridge goroutine stop the watcher

	if _, err := c.CreateArtifact(t.Context(), "wc", []byte(`{"after":"cancel"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-got:
		t.Errorf("received a change after ctx cancel; watch should have stopped: %+v", ch)
	case <-time.After(700 * time.Millisecond):
		// good: no delivery after cancellation
	}
}
