package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// ArchiveDeps bundles the dependencies the archive handler needs. It is
// the same shape as KillDeps because archive must be able to stop a live
// incarnation before flipping lifecycle to archived (otherwise an
// operator archiving a running agent would leak its container).
type ArchiveDeps struct {
	Definitions  AgentMutableKV
	Incarnations AgentMutableKV
	Containers   ContainerRunner
	// Volumes, when non-nil, lets the archive handler clean up the
	// per-agent claude_seed copy-on-spawn volume. Best-effort: a
	// volume-remove failure is logged but does not abort the archive.
	// When nil (test harnesses without a volume manager) the cleanup
	// step is silently skipped.
	Volumes VolumeManager
	Now     func() time.Time
}

// NewArchiveAgent returns a Handler for the archive_agent verb. Flow:
//
//  1. Decode args.
//  2. Look up the AgentDefinition; error 404 if missing.
//  3. If lifecycle is already "archived", emit OK (idempotent).
//  4. Find the live incarnation (if any) and stop its container; mark
//     the incarnation exited. Same shape as kill_agent so an operator
//     can archive a running agent in one shot.
//  5. Flip the definition's lifecycle to "archived" + bump version.
//     This is the step that releases the agent's name: agentNameInUse
//     in spawn.go excludes archived definitions from the uniqueness
//     scan, so a subsequent spawn under the same name succeeds.
//  6. Reply {ok: true}.
//
// See plans/issues/bug-kill-doesnt-release-name.md and
// plans/issues/feat-agents-archive-cli-verb.md for the motivating
// gaps. architecture.md §2 ("Identity rules") spells out the
// name-release semantics.
func NewArchiveAgent(deps ArchiveDeps) rpc.Handler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.ArchiveAgentRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode archive_agent payload: %v", err))
		}
		if args.AgentID == uuid.Nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "agent_id is required")
		}
		grace := time.Duration(args.GraceSeconds) * time.Second
		if grace <= 0 {
			grace = defaultGraceSeconds * time.Second
		}

		// 2. Load definition.
		defEntry, err := deps.Definitions.Get(ctx, args.AgentID.String())
		if err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return emitErr(emit, sextantproto.ErrCodeAgentNotFound,
					fmt.Sprintf("no agent with uuid %s", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("load definition: %v", err))
		}
		var def sextantproto.AgentDefinition
		if err := json.Unmarshal(defEntry.Value(), &def); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("decode definition: %v", err))
		}
		// initialDefRevision pins the revision we just read so the
		// final def write (step 5) can refuse to clobber a concurrent
		// restart_agent / kill_agent / update_agent commit. Mirrors
		// the kill_agent migration in commit ceb0bb2; symmetric to the
		// CAS guard restart_agent already has against archive.
		// Without this, archive would walk a stale def for ~ms while
		// stopping the live container, then plain-Put lifecycle=archived
		// over whatever a concurrent restart_agent / update_agent
		// committed in that window.
		initialDefRevision := defEntry.Revision()

		// 3. Idempotent on already-archived.
		if def.Lifecycle == sextantproto.LifecycleArchived {
			return emitOK(emit, sextantproto.ArchiveAgentResponse{OK: true})
		}

		// 4. Stop live incarnation, if any. This mirrors kill_agent's
		// shape so an operator who archives a running agent gets the
		// same cleanup as kill+archive but in one trip. Containers may
		// be nil in tests that only exercise the lifecycle flip path —
		// the live-incarnation walk still happens so a stray incarnation
		// record gets marked exited.
		now := deps.Now().UTC()
		inc, incKey, err := findLiveIncarnation(ctx, deps.Incarnations, args.AgentID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("find live incarnation: %v", err))
		}
		if inc != nil {
			if inc.ContainerID != "" && deps.Containers != nil {
				if err := deps.Containers.Stop(ctx, inc.ContainerID, grace); err != nil {
					return emitErr(emit, sextantproto.ErrCodeInternal,
						fmt.Sprintf("stop container %s: %v", inc.ContainerID, err))
				}
			}
			ended := sextantproto.AtTimestamp(now)
			inc.State = sextantproto.IncarnationExited
			inc.EndedAt = &ended
			if err := putJSON(ctx, deps.Incarnations, incKey, *inc); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("update incarnation: %v", err))
			}
		}

		// 5. Flip the definition to archived via CAS. This is the step
		// that releases the name — agentNameInUse skips archived
		// entries. CAS on initialDefRevision so a concurrent
		// restart_agent / kill_agent / update_agent commit between
		// step 2's Get and this write is detected: we BAIL rather
		// than overwrite their intent. No side-effect rollback is
		// needed (the incarnation stop is already terminal — see the
		// ticket's "BAIL still makes sense" reasoning).
		def.Lifecycle = sextantproto.LifecycleArchived
		def.Version++
		def.UpdatedAt = sextantproto.AtTimestamp(now)
		raw, mErr := json.Marshal(def)
		if mErr != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("marshal definition: %v", mErr))
		}
		if _, err := deps.Definitions.Update(ctx, args.AgentID.String(), raw, initialDefRevision); err != nil {
			if errors.Is(err, jetstream.ErrKeyExists) {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("agent %s definition changed during archive (concurrent restart/kill/update); re-issue archive if still appropriate", args.AgentID))
			}
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("flip lifecycle to archived: %v", err))
		}
		// 6. Clean up per-agent volumes (claude_seed copy-on-spawn).
		// Best-effort: the archive succeeded; failing to delete the
		// volume becomes an operator chore, not a spawn-blocking error.
		// We unconditionally attempt the remove — RemoveVolume is
		// idempotent on "no such volume" so an agent that never had a
		// claude_seed volume gets a cheap no-op.
		if deps.Volumes != nil {
			volName := ClaudeSeedVolumeName(args.AgentID)
			if err := deps.Volumes.RemoveVolume(ctx, volName, true); err != nil {
				// Log via stderr — matches the pattern in spawn.go's
				// history-insert failure path.
				fmt.Fprintf(os.Stderr, "archive_agent: remove volume %s: %v\n", volName, err)
			}
		}
		return emitOK(emit, sextantproto.ArchiveAgentResponse{OK: true})
	}
}

// Compile-time sanity check that the verb constant exists; mirrors the
// pattern kill.go uses to keep the import graph honest.
var _ = rpc.VerbArchiveAgent
