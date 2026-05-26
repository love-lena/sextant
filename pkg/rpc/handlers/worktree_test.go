package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/worktree"
)

// fakeWorktreeMgr is a stand-in for worktree.Manager. Each method
// returns whatever the test pre-loaded into the corresponding field,
// and records the args it received so tests can assert dispatch
// reached the right call.
type fakeWorktreeMgr struct {
	createInfo sextantproto.WorktreeInfo
	createErr  error
	destroyErr error
	listOut    []sextantproto.WorktreeInfo
	listErr    error
	diffOut    string
	diffErr    error
	mergeOut   worktree.MergeResult
	mergeErr   error

	calls []string
}

func (f *fakeWorktreeMgr) Create(_ context.Context, name, baseBranch string, owner uuid.UUID) (sextantproto.WorktreeInfo, error) {
	f.calls = append(f.calls, "create:"+name+":"+baseBranch+":"+owner.String())
	if f.createErr != nil {
		return sextantproto.WorktreeInfo{}, f.createErr
	}
	out := f.createInfo
	if out.Name == "" {
		out.Name = name
		out.Branch = name
		out.BaseBranch = baseBranch
		out.Status = sextantproto.WorktreeStatusActive
	}
	return out, nil
}

func (f *fakeWorktreeMgr) Destroy(_ context.Context, name string, force bool) error {
	f.calls = append(f.calls, "destroy:"+name)
	_ = force
	return f.destroyErr
}

func (f *fakeWorktreeMgr) List(_ context.Context) ([]sextantproto.WorktreeInfo, error) {
	f.calls = append(f.calls, "list")
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return []sextantproto.WorktreeInfo{}, nil
	}
	return f.listOut, nil
}

func (f *fakeWorktreeMgr) Diff(_ context.Context, name, against string) (string, error) {
	f.calls = append(f.calls, "diff:"+name+":"+against)
	return f.diffOut, f.diffErr
}

func (f *fakeWorktreeMgr) Merge(_ context.Context, name, target string) (worktree.MergeResult, error) {
	f.calls = append(f.calls, "merge:"+name+":"+target)
	return f.mergeOut, f.mergeErr
}

func TestWorktreeCreateHappyPath(t *testing.T) {
	mgr := &fakeWorktreeMgr{}
	h := handlers.NewWorktreeCreate(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeCreateRequest{Name: "feat-x-001", BaseBranch: "main"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.WorktreeCreateResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Worktree.Name != "feat-x-001" {
		t.Errorf("Name = %q", resp.Worktree.Name)
	}
	if len(mgr.calls) != 1 || mgr.calls[0] != "create:feat-x-001:main:"+uuid.Nil.String() {
		t.Errorf("calls = %v", mgr.calls)
	}
}

func TestWorktreeCreateMapsInvalidName(t *testing.T) {
	mgr := &fakeWorktreeMgr{createErr: worktree.ErrInvalidName}
	h := handlers.NewWorktreeCreate(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeCreateRequest{Name: "nope"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeBadRequest {
		t.Fatalf("Error = %+v, want bad_request", cap.resp.Error)
	}
}

func TestWorktreeDestroyHappyPath(t *testing.T) {
	mgr := &fakeWorktreeMgr{}
	h := handlers.NewWorktreeDestroy(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeDestroyRequest{Name: "feat-x-001", Force: true})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.WorktreeDestroyResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Errorf("OK = false")
	}
}

func TestWorktreeDestroyMapsNotFound(t *testing.T) {
	mgr := &fakeWorktreeMgr{destroyErr: errors.New("worktree: not found: feat-x-001")}
	// Use a sentinel that wraps ErrWorktreeNotFound.
	mgr.destroyErr = wrap(worktree.ErrWorktreeNotFound, "feat-x-001")
	h := handlers.NewWorktreeDestroy(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeDestroyRequest{Name: "feat-x-001"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeNotFound {
		t.Fatalf("Error = %+v, want not_found", cap.resp.Error)
	}
}

func TestWorktreeListReturnsAll(t *testing.T) {
	mgr := &fakeWorktreeMgr{
		listOut: []sextantproto.WorktreeInfo{
			{Name: "feat-a-001", Branch: "feat-a-001", Status: sextantproto.WorktreeStatusActive},
			{Name: "feat-b-001", Branch: "feat-b-001", Status: sextantproto.WorktreeStatusMerged},
		},
	}
	h := handlers.NewWorktreeList(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeListRequest{})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.WorktreeListResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Worktrees) != 2 {
		t.Errorf("len = %d", len(resp.Worktrees))
	}
}

func TestWorktreeMergeCleanPath(t *testing.T) {
	mgr := &fakeWorktreeMgr{
		mergeOut: worktree.MergeResult{
			OK:     true,
			Branch: "feat-x-001",
			Target: "main",
		},
	}
	h := handlers.NewWorktreeMerge(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeMergeRequest{Name: "feat-x-001", Target: "main"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.WorktreeMergeResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || resp.Branch != "feat-x-001" || resp.Target != "main" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestWorktreeMergeConflictPath(t *testing.T) {
	mgr := &fakeWorktreeMgr{
		mergeOut: worktree.MergeResult{
			OK:        false,
			Branch:    "feat-x-001",
			Target:    "main",
			Conflicts: []string{"a.txt", "b/c.txt"},
		},
	}
	h := handlers.NewWorktreeMerge(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeMergeRequest{Name: "feat-x-001"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v (want clean response with OK=false)", cap.resp.Error)
	}
	var resp sextantproto.WorktreeMergeResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Error("OK should be false on conflict")
	}
	if len(resp.Conflicts) != 2 {
		t.Errorf("Conflicts = %v", resp.Conflicts)
	}
}

func TestWorktreeDiffReturnsBytes(t *testing.T) {
	mgr := &fakeWorktreeMgr{diffOut: "diff --git a/x b/x\n+hi\n"}
	h := handlers.NewWorktreeDiff(handlers.WorktreeDeps{Manager: mgr})
	cap := &captureEmit{}
	req := makeReq(t, sextantproto.WorktreeDiffRequest{Name: "feat-x-001"})
	if err := h(context.Background(), req, cap.emit()); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if cap.resp.Error != nil {
		t.Fatalf("Error = %+v", cap.resp.Error)
	}
	var resp sextantproto.WorktreeDiffResponse
	if err := json.Unmarshal(cap.resp.Result, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Diff != "diff --git a/x b/x\n+hi\n" {
		t.Errorf("Diff = %q", resp.Diff)
	}
}

func TestWorktreeHandlersRejectNilManager(t *testing.T) {
	deps := handlers.WorktreeDeps{Manager: nil}
	cases := []struct {
		name string
		h    func() error
	}{
		{"create", func() error {
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.WorktreeCreateRequest{Name: "feat-x-001"})
			if err := handlers.NewWorktreeCreate(deps)(context.Background(), req, cap.emit()); err != nil {
				return err
			}
			if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
				t.Errorf("create: Error = %+v", cap.resp.Error)
			}
			return nil
		}},
		{"destroy", func() error {
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.WorktreeDestroyRequest{Name: "feat-x-001"})
			if err := handlers.NewWorktreeDestroy(deps)(context.Background(), req, cap.emit()); err != nil {
				return err
			}
			if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
				t.Errorf("destroy: Error = %+v", cap.resp.Error)
			}
			return nil
		}},
		{"list", func() error {
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.WorktreeListRequest{})
			if err := handlers.NewWorktreeList(deps)(context.Background(), req, cap.emit()); err != nil {
				return err
			}
			if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
				t.Errorf("list: Error = %+v", cap.resp.Error)
			}
			return nil
		}},
		{"merge", func() error {
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.WorktreeMergeRequest{Name: "feat-x-001"})
			if err := handlers.NewWorktreeMerge(deps)(context.Background(), req, cap.emit()); err != nil {
				return err
			}
			if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
				t.Errorf("merge: Error = %+v", cap.resp.Error)
			}
			return nil
		}},
		{"diff", func() error {
			cap := &captureEmit{}
			req := makeReq(t, sextantproto.WorktreeDiffRequest{Name: "feat-x-001"})
			if err := handlers.NewWorktreeDiff(deps)(context.Background(), req, cap.emit()); err != nil {
				return err
			}
			if cap.resp.Error == nil || cap.resp.Error.Code != sextantproto.ErrCodeInternal {
				t.Errorf("diff: Error = %+v", cap.resp.Error)
			}
			return nil
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.h(); err != nil {
				t.Fatalf("handler: %v", err)
			}
		})
	}
}

// wrap is a tiny errors.New + %w shim used by the handler tests.
func wrap(sentinel error, name string) error {
	return errors.Join(sentinel, errors.New(name))
}
