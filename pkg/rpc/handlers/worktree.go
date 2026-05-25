package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
	"github.com/love-lena/sextant-initial/pkg/worktree"
)

// WorktreeManager is the narrow surface the M14 RPC handlers call on
// pkg/worktree. Mirrored as an interface so tests can substitute a
// fake without spinning up a real git repo + KV.
type WorktreeManager interface {
	Create(ctx context.Context, name, baseBranch string, owningAgent uuid.UUID) (sextantproto.WorktreeInfo, error)
	Destroy(ctx context.Context, name string, force bool) error
	List(ctx context.Context) ([]sextantproto.WorktreeInfo, error)
	Diff(ctx context.Context, name, against string) (string, error)
	Merge(ctx context.Context, name, target string) (worktree.MergeResult, error)
}

// WorktreeDeps bundles the inputs the worktree handlers need.
// Manager is required. OwningAgentResolver maps an envelope's caller
// identity to the UUID we record in WorktreeInfo.OwningAgent; nil
// keeps OwningAgent at uuid.Nil (i.e. "operator-created").
type WorktreeDeps struct {
	Manager WorktreeManager
}

// NewWorktreeCreate returns a Handler for worktree_create.
func NewWorktreeCreate(deps WorktreeDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		if deps.Manager == nil {
			return emitErr(emit, sextantproto.ErrCodeInternal, "worktree manager not configured")
		}
		var args sextantproto.WorktreeCreateRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode worktree_create payload: %v", err))
		}
		// Operator path stamps zero UUID for OwningAgent; the MCP
		// dispatcher passes a non-zero UUID through a wrapper when the
		// caller is an agent (TODO: M14 keeps it simple — operator-only
		// for now; agent-caller plumbing arrives with the first agent
		// MCP smoke test in M15).
		info, err := deps.Manager.Create(ctx, args.Name, args.BaseBranch, uuid.Nil)
		if err != nil {
			return mapWorktreeErr(emit, err)
		}
		return emitOK(emit, sextantproto.WorktreeCreateResponse{Worktree: info})
	}
}

// NewWorktreeDestroy returns a Handler for worktree_destroy.
func NewWorktreeDestroy(deps WorktreeDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		if deps.Manager == nil {
			return emitErr(emit, sextantproto.ErrCodeInternal, "worktree manager not configured")
		}
		var args sextantproto.WorktreeDestroyRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode worktree_destroy payload: %v", err))
		}
		if err := deps.Manager.Destroy(ctx, args.Name, args.Force); err != nil {
			return mapWorktreeErr(emit, err)
		}
		return emitOK(emit, sextantproto.WorktreeDestroyResponse{OK: true})
	}
}

// NewWorktreeList returns a Handler for worktree_list.
func NewWorktreeList(deps WorktreeDeps) rpc.Handler {
	return func(ctx context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		if deps.Manager == nil {
			return emitErr(emit, sextantproto.ErrCodeInternal, "worktree manager not configured")
		}
		list, err := deps.Manager.List(ctx)
		if err != nil {
			return mapWorktreeErr(emit, err)
		}
		return emitOK(emit, sextantproto.WorktreeListResponse{Worktrees: list})
	}
}

// NewWorktreeMerge returns a Handler for worktree_merge.
func NewWorktreeMerge(deps WorktreeDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		if deps.Manager == nil {
			return emitErr(emit, sextantproto.ErrCodeInternal, "worktree manager not configured")
		}
		var args sextantproto.WorktreeMergeRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode worktree_merge payload: %v", err))
		}
		res, err := deps.Manager.Merge(ctx, args.Name, args.Target)
		if err != nil {
			return mapWorktreeErr(emit, err)
		}
		return emitOK(emit, sextantproto.WorktreeMergeResponse{
			OK:        res.OK,
			Branch:    res.Branch,
			Target:    res.Target,
			Conflicts: res.Conflicts,
		})
	}
}

// NewWorktreeDiff returns a Handler for worktree_diff.
func NewWorktreeDiff(deps WorktreeDeps) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		if deps.Manager == nil {
			return emitErr(emit, sextantproto.ErrCodeInternal, "worktree manager not configured")
		}
		var args sextantproto.WorktreeDiffRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode worktree_diff payload: %v", err))
		}
		diff, err := deps.Manager.Diff(ctx, args.Name, args.Against)
		if err != nil {
			return mapWorktreeErr(emit, err)
		}
		return emitOK(emit, sextantproto.WorktreeDiffResponse{Diff: diff})
	}
}

// mapWorktreeErr translates a pkg/worktree error into a structured
// RPCError code. Sentinel-based: an unknown error becomes "internal".
func mapWorktreeErr(emit func(sextantproto.RPCResponse), err error) error {
	switch {
	case errors.Is(err, worktree.ErrWorktreeNotFound):
		return emitErr(emit, sextantproto.ErrCodeNotFound, err.Error())
	case errors.Is(err, worktree.ErrInvalidName):
		return emitErr(emit, sextantproto.ErrCodeBadRequest, err.Error())
	case errors.Is(err, worktree.ErrAlreadyExists):
		return emitErr(emit, sextantproto.ErrCodeBadRequest, err.Error())
	case errors.Is(err, worktree.ErrStatusGuard):
		return emitErr(emit, sextantproto.ErrCodeBadRequest, err.Error())
	case errors.Is(err, worktree.ErrLockHeld):
		return emitErr(emit, sextantproto.ErrCodeBadRequest, err.Error())
	default:
		return emitErr(emit, sextantproto.ErrCodeInternal, err.Error())
	}
}
