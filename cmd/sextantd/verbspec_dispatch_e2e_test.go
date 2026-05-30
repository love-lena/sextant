package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// minimalReqFor returns a minimal request payload for verb — enough to
// reach the handler. The handler may reject it (bad_request,
// agent_not_found, etc.); the dispatch e2e only asserts the verb is
// *registered and routed*, which any reply other than unknown_verb
// proves. Container verbs get a random agent_id so they fall through to
// agent_not_found without needing a live container (no docker).
func minimalReqFor(verb string) any {
	randAgent := uuid.New()
	switch verb {
	case rpc.VerbListAgents:
		return sextantproto.ListAgentsRequest{}
	case rpc.VerbGetAgentStatus:
		return sextantproto.GetAgentStatusRequest{AgentID: randAgent}
	case rpc.VerbQueryHistory:
		return sextantproto.QueryHistoryRequest{Limit: 1}
	case rpc.VerbQueryAudit:
		return sextantproto.QueryAuditRequest{Limit: 1}
	case rpc.VerbQueryTrace:
		return sextantproto.QueryTraceRequest{TraceID: "deadbeef"}
	case rpc.VerbGetVersion:
		return sextantproto.GetVersionRequest{}
	case rpc.VerbSpawnAgent:
		return sextantproto.SpawnAgentRequest{} // empty name → bad_request
	case rpc.VerbKillAgent:
		return sextantproto.KillAgentRequest{AgentID: randAgent}
	case rpc.VerbArchiveAgent:
		return sextantproto.ArchiveAgentRequest{AgentID: randAgent}
	case rpc.VerbPromptAgent:
		return sextantproto.PromptAgentRequest{AgentID: randAgent, Content: "hi"}
	case rpc.VerbRestartAgent:
		return sextantproto.RestartAgentRequest{AgentID: randAgent}
	case rpc.VerbReadFile:
		return sextantproto.ReadFileRequest{AgentID: randAgent, Path: "/etc/hosts"}
	case rpc.VerbListDir:
		return sextantproto.ListDirRequest{AgentID: randAgent, Path: "/"}
	case rpc.VerbStat:
		return sextantproto.StatRequest{AgentID: randAgent, Path: "/"}
	case rpc.VerbExecInContainer:
		return sextantproto.ExecInContainerRequest{AgentID: randAgent, Cmd: []string{"true"}}
	case rpc.VerbWorktreeCreate:
		// Empty name → validation error (a dispatch), so the probe does
		// not create a real worktree as a side effect.
		return sextantproto.WorktreeCreateRequest{}
	case rpc.VerbWorktreeDestroy:
		return sextantproto.WorktreeDestroyRequest{Name: "definitely-not-a-worktree"}
	case rpc.VerbWorktreeList:
		return sextantproto.WorktreeListRequest{}
	case rpc.VerbWorktreeMerge:
		return sextantproto.WorktreeMergeRequest{Name: "definitely-not-a-worktree"}
	case rpc.VerbWorktreeDiff:
		return sextantproto.WorktreeDiffRequest{Name: "definitely-not-a-worktree"}
	default:
		// A new verb with no probe payload: send an empty struct. If the
		// handler rejects it, that's still a non-unknown-verb dispatch.
		return struct{}{}
	}
}

// TestEveryVerbDispatchesE2E is the C2 acceptance e2e: a real daemon
// boots (NATS + ClickHouse + RPC + worktree runtime), and EVERY verb in
// the single VerbSpec table dispatches — the reply is never unknown_verb,
// proving each verb's handler is registered after the table-driven staged
// registration. The deeper behavioral assertions (container side effects,
// lifecycle envelopes, worktree git ops) live in the milestone tests
// (TestM11SpawnFlowAcceptance, TestM14WorktreeAcceptance, etc.); this test
// guards the table → registration projection for the whole surface at
// once.
//
// It uses the worktree-enabled harness so the PhaseWorktree verbs are
// registered. It needs nats-server + clickhouse (requireBins via the
// harness) and git, but NOT docker: container verbs fall through to
// agent_not_found against a random agent_id, which is still a successful
// dispatch.
func TestEveryVerbDispatchesE2E(t *testing.T) {
	h, _, _ := startDaemonHarnessWithWorktree(t)
	cli := rpcClient(t, h)

	for _, spec := range rpc.VerbSpecs {
		t.Run(spec.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err := cli.RPC(ctx, spec.Name, minimalReqFor(spec.Name), nil)
			if err == nil {
				// Clean success is a dispatch — fine.
				return
			}
			var rerr *client.RPCError
			if !errors.As(err, &rerr) {
				// Transport error (timeout, etc.) — not a dispatch proof.
				t.Fatalf("verb %q: non-RPC error %v\n--- daemon log ---\n%s", spec.Name, err, h.tail(t))
			}
			if rerr.Code == sextantproto.ErrCodeUnknownVerb {
				t.Fatalf("verb %q replied unknown_verb — handler not registered (table → registration drift)\n--- daemon log ---\n%s",
					spec.Name, h.tail(t))
			}
			// Any other structured RPCError means the handler ran and
			// rejected the probe payload — that's a successful dispatch.
			t.Logf("verb %q dispatched (handler reply code=%q)", spec.Name, rerr.Code)
		})
	}
}
