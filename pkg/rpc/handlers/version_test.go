package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestGetVersionReturnsCapturedStartedAt exercises the canonical path:
// the handler closes over the daemon's startedAt + the build metadata
// and emits a terminal success envelope. The wire shape mirrors the
// sextantproto.GetVersionResponse contract — fields that downstream
// `sextant doctor` formatting depends on must round-trip 1:1.
func TestGetVersionReturnsCapturedStartedAt(t *testing.T) {
	started := time.Date(2026, 5, 28, 10, 32, 11, 0, time.UTC)
	h := handlers.NewGetVersion(handlers.VersionDeps{
		StartedAt:     started,
		DaemonVersion: "v0.2.0",
		Commit:        "abc1234",
		ProtoVersion:  "0.2.0",
		PIDFn:         func() int { return 12345 },
	})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetVersionRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.hits != 1 {
		t.Fatalf("emit hits = %d, want 1", cap.hits)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %v, want nil", cap.resp.Error)
	}
	if !cap.resp.Terminal {
		t.Error("Terminal must be true")
	}
	var resp sextantproto.GetVersionResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DaemonVersion != "v0.2.0" {
		t.Errorf("DaemonVersion = %q, want v0.2.0", resp.DaemonVersion)
	}
	if resp.Commit != "abc1234" {
		t.Errorf("Commit = %q, want abc1234", resp.Commit)
	}
	if resp.ProtoVersion != "0.2.0" {
		t.Errorf("ProtoVersion = %q, want 0.2.0", resp.ProtoVersion)
	}
	if !resp.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", resp.StartedAt, started)
	}
	if resp.PID != 12345 {
		t.Errorf("PID = %d, want 12345", resp.PID)
	}
}

// TestGetVersionEmptyPayloadAccepted — the verb is parameter-less, so
// an empty payload must succeed. Doctor passes an empty struct; we
// guard the byte-empty path here as well.
func TestGetVersionEmptyPayloadAccepted(t *testing.T) {
	h := handlers.NewGetVersion(handlers.VersionDeps{
		StartedAt:     time.Now(),
		DaemonVersion: "dev",
		Commit:        "unknown",
		ProtoVersion:  "0.2.0",
		PIDFn:         func() int { return 1 },
	})
	cap := &captureEmit{}
	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "test"}
	req := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, from, nil)
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %v, want nil", cap.resp.Error)
	}
}

// TestGetVersionDefaultsToPackageGlobals — when the dep bag is zero,
// the handler falls back to pkgversion.{Version,Commit} and
// sextantproto.ProtoVersion. The defaults under `go test` are "dev" /
// "unknown" / "0.2.0" respectively (no -ldflags).
func TestGetVersionDefaultsToPackageGlobals(t *testing.T) {
	h := handlers.NewGetVersion(handlers.VersionDeps{StartedAt: time.Unix(0, 0)})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.GetVersionRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	var resp sextantproto.GetVersionResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// `go test` doesn't pass -ldflags, so the package globals carry
	// their literal defaults. We assert the obvious ones rather than
	// re-import the version package to compare — that would just be a
	// tautology.
	if resp.DaemonVersion == "" {
		t.Error("DaemonVersion must default from pkg/version when dep bag is empty")
	}
	if resp.ProtoVersion != sextantproto.ProtoVersion {
		t.Errorf("ProtoVersion = %q, want %q", resp.ProtoVersion, sextantproto.ProtoVersion)
	}
	if resp.PID <= 0 {
		t.Errorf("PID = %d, want >0 (os.Getpid default)", resp.PID)
	}
}
