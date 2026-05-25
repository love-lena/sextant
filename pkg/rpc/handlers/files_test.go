package handlers_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/containermgr"
	"github.com/love-lena/sextant-initial/pkg/rpc/handlers"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// stubExec is a fake ContainerExecRunner. It dispatches on the first
// argv element so tests can pre-register responses per command
// (cat/ls/stat/etc.) without bringing real Docker up.
type stubExec struct {
	mu sync.Mutex
	// responses maps "cmd arg1 arg2 ..." (space-joined) to the result.
	// We use the joined form so callers can write expressive cases
	// without an exact-match per-arg map.
	responses map[string]containermgr.ExecResult
	// lastSpec captures the most recent spec for assertions.
	lastSpec containermgr.ExecSpec
}

func (s *stubExec) Exec(_ context.Context, _ string, spec containermgr.ExecSpec) (containermgr.ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSpec = spec
	key := strings.Join(spec.Cmd, " ")
	if r, ok := s.responses[key]; ok {
		return r, nil
	}
	// Default: empty output, exit 0.
	return containermgr.ExecResult{}, nil
}

// seedAgent creates a live AgentDefinition + AgentIncarnation pair in
// the supplied fake KV pair so the files handlers can resolve the
// container ID.
func seedAgent(t *testing.T, defs, incs *fakeMutableKV, agentID uuid.UUID, containerID string) {
	t.Helper()
	def := sextantproto.AgentDefinition{
		UUID:      agentID,
		Name:      "alpha",
		Lifecycle: sextantproto.LifecycleRunning,
		Version:   1,
	}
	raw, _ := json.Marshal(def)
	if _, err := defs.Put(context.Background(), agentID.String(), raw); err != nil {
		t.Fatalf("seed def: %v", err)
	}
	inc := sextantproto.AgentIncarnation{
		IncarnationID: uuid.New(),
		AgentUUID:     agentID,
		ContainerID:   containerID,
		State:         sextantproto.IncarnationStarting,
	}
	raw, _ = json.Marshal(inc)
	if _, err := incs.Put(context.Background(), inc.IncarnationID.String(), raw); err != nil {
		t.Fatalf("seed inc: %v", err)
	}
}

func TestReadFileRoundTripsContent(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	want := []byte("hello, world\n")
	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		// M12 read_file does a size pre-check via stat, then cat.
		"stat -c %s /workspace/note.txt": {Stdout: []byte("13\n"), ExitCode: 0},
		"cat /workspace/note.txt":        {Stdout: want, ExitCode: 0},
	}}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/workspace/note.txt",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.ReadFileResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(resp.Content) != string(want) {
		t.Errorf("Content = %q, want %q", resp.Content, want)
	}
	if resp.ContentType == "" {
		t.Error("ContentType is empty (sniffer should have set something)")
	}
}

func TestReadFileMissingFileReturnsBadRequest(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	// stat returns the canonical error on a missing path; read_file
	// rejects up-front before it ever touches cat.
	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"stat -c %s /nope.txt": {
			Stderr:   []byte("stat: cannot statx '/nope.txt': No such file or directory\n"),
			ExitCode: 1,
		},
	}}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/nope.txt",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Code = %q, want bad_request", cap.resp.Error.Code)
	}
	if !strings.Contains(cap.resp.Error.Message, "No such file") {
		t.Errorf("Message = %q, want stderr passthrough", cap.resp.Error.Message)
	}
}

// TestReadFileRejectsFileLargerThanCap pins the M12 size-cap fix.
// Pre-fix the handler claimed a 16 MiB cap in its docstring but
// returned cat's stdout unconstrained — a 50 MiB file would have
// blown past NATS max_payload silently. Post-fix the size pre-check
// rejects with a structured bad_request before any bytes leave the
// container.
func TestReadFileRejectsFileLargerThanCap(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	// 50 MiB file — well past the 16 MiB cap.
	const oversize = 50 * 1024 * 1024
	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"stat -c %s /workspace/huge.bin": {
			Stdout:   []byte(strconv.FormatInt(oversize, 10) + "\n"),
			ExitCode: 0,
		},
		// If the handler ignored the cap and called cat, this would
		// return 50 MiB of zeroes. The test asserts cat is NEVER called
		// by checking lastSpec.Cmd[0] after the handler runs.
		"cat /workspace/huge.bin": {
			Stdout:   make([]byte, oversize),
			ExitCode: 0,
		},
	}}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/workspace/huge.bin",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected an error for oversized file")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Code = %q, want bad_request", cap.resp.Error.Code)
	}
	if !strings.Contains(cap.resp.Error.Message, "cap") {
		t.Errorf("Message = %q, want the cap rationale", cap.resp.Error.Message)
	}
	if !strings.Contains(cap.resp.Error.Message, "read_file_stream") {
		t.Errorf("Message = %q, should point at read_file_stream", cap.resp.Error.Message)
	}
	// Critical: the handler must not have invoked cat. We assert via
	// the runner's lastSpec recording the most recent Exec call —
	// it should still be the stat call.
	runner.mu.Lock()
	last := runner.lastSpec.Cmd
	runner.mu.Unlock()
	if len(last) == 0 || last[0] != "stat" {
		t.Errorf("last exec = %v, want stat (cat must not have been invoked for an oversize file)", last)
	}
}

// TestReadFileAcceptsFileAtCap confirms the boundary: a file exactly
// at ReadFileMaxBytes is accepted.
func TestReadFileAcceptsFileAtCap(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	atCap := handlers.ReadFileMaxBytes
	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"stat -c %s /workspace/right-at-cap.bin": {
			Stdout:   []byte(strconv.FormatInt(atCap, 10) + "\n"),
			ExitCode: 0,
		},
		"cat /workspace/right-at-cap.bin": {
			Stdout:   make([]byte, atCap),
			ExitCode: 0,
		},
	}}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/workspace/right-at-cap.bin",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v (a file exactly at the cap must be accepted)", cap.resp.Error)
	}
}

func TestReadFileUnknownAgentReturnsAgentNotFound(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	runner := &stubExec{}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: uuid.New(), Path: "/etc/hosts",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
}

func TestReadFileNoLiveIncarnationReturnsBadRequest(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	// Seed only the def — no incarnation.
	def := sextantproto.AgentDefinition{
		UUID:      agentID,
		Name:      "alpha",
		Lifecycle: sextantproto.LifecycleDefined,
	}
	raw, _ := json.Marshal(def)
	if _, err := defs.Put(context.Background(), agentID.String(), raw); err != nil {
		t.Fatalf("seed: %v", err)
	}

	runner := &stubExec{}
	h := handlers.NewReadFile(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ReadFileRequest{
		AgentID: agentID, Path: "/etc/hosts",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil {
		t.Fatal("expected error")
	}
	if cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Errorf("Code = %q, want bad_request", cap.resp.Error.Code)
	}
}

func TestListDirParsesLsOutput(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"ls -1Ap /workspace": {
			Stdout: []byte("README.md\nsrc/\nMakefile\n"),
		},
	}}
	h := handlers.NewListDir(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ListDirRequest{
		AgentID: agentID, Path: "/workspace",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.ListDirResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("Entries count = %d, want 3", len(resp.Entries))
	}
	want := []struct {
		name  string
		isDir bool
	}{
		{"README.md", false},
		{"src", true},
		{"Makefile", false},
	}
	for i, w := range want {
		if resp.Entries[i].Name != w.name || resp.Entries[i].IsDir != w.isDir {
			t.Errorf("Entries[%d] = %+v, want {%s %v}", i, resp.Entries[i], w.name, w.isDir)
		}
	}
}

func TestStatParsesStatOutput(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"stat -c %s|%a|%F|%n /workspace/README.md": {
			Stdout: []byte("1234|644|regular file|/workspace/README.md\n"),
		},
	}}
	h := handlers.NewStat(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.StatRequest{
		AgentID: agentID, Path: "/workspace/README.md",
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.StatResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "README.md" || resp.Size != 1234 || resp.Mode != "644" || resp.IsDir {
		t.Errorf("StatResponse = %+v, want {README.md 1234 644 false}", resp)
	}
}

func TestExecInContainerPassesThroughResult(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"echo hi": {Stdout: []byte("hi\n"), ExitCode: 0},
	}}
	h := handlers.NewExecInContainer(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ExecInContainerRequest{
		AgentID: agentID, Cmd: []string{"echo", "hi"},
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.ExecInContainerResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Stdout != "hi\n" || resp.ExitCode != 0 {
		t.Errorf("Resp = %+v", resp)
	}
}

func TestExecInContainerNonZeroExitIsNotAnRPCError(t *testing.T) {
	defs := newFakeMutableKV()
	incs := newFakeMutableKV()
	agentID := uuid.New()
	seedAgent(t, defs, incs, agentID, "ctr-123")

	runner := &stubExec{responses: map[string]containermgr.ExecResult{
		"false": {ExitCode: 1},
	}}
	h := handlers.NewExecInContainer(handlers.FilesDeps{
		Definitions:  defs,
		Incarnations: incs,
		Containers:   runner,
	})
	cap := &captureEmit{}
	if err := h(context.Background(), makeReq(t, sextantproto.ExecInContainerRequest{
		AgentID: agentID, Cmd: []string{"false"},
	}), cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	// The non-zero exit code surfaces via ExecInContainerResponse.ExitCode,
	// not as an RPC error.
	if cap.resp.Error != nil {
		t.Fatalf("non-zero exit should not be an RPC error: %+v", cap.resp.Error)
	}
	var resp sextantproto.ExecInContainerResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", resp.ExitCode)
	}
}
