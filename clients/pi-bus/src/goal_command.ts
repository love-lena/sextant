// The /set-goal command (AC#2, AC#6). It moves one acceptance criterion of a
// shared goal — and it does so THROUGH the goals convention (@sextant/conv-goals,
// TASK-175), never by hand-rolling the artifact write. That is the point of the
// criterion: a co-equal client (a pi agent) drives a goal the SAME way the Go
// client and the dash do, through the one convention library, so the goal it
// moves is the same goal.<id> artifact the dash reads and re-renders (closing the
// loop to TASK-173 / AC#6).
//
// setCriterion's Ops seam is { getArtifact, updateArtifact, publish } — exactly
// the subset of the SDK Client the verb needs, and the Client satisfies it
// structurally (no adapter). So the command is a thin parse-of-args around the
// convention call; all the goal mechanics (the proof-preserving rewrite, the
// goal.update announcement on msg.topic.goals that the dash watches) live in the
// convention, in one place, per ADR-0041.
//
// Usage:
//   /set-goal <criterionId> <status> [headline...]         (uses the default goal)
//   /set-goal <goalId> <criterionId> <status> [headline...]
// status is one of: met | in-progress | waiting-on-you | blocked | not-started.

import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";
import type { Client } from "@sextant/sdk";
import {
  setCriterion,
  SetCriterionError,
  StatusBlocked,
  StatusInProgress,
  StatusMet,
  StatusNotStarted,
  StatusWaitingOnYou,
} from "@sextant/conv-goals";

const VALID_STATUS = new Set([StatusMet, StatusInProgress, StatusWaitingOnYou, StatusBlocked, StatusNotStarted]);

// registerGoalCommand wires /set-goal. getClient resolves the live client (the
// Ops seam); defaultGoalId is the configured goal used when the args omit one.
export function registerGoalCommand(
  pi: ExtensionAPI,
  opts: { getClient: () => Client | undefined; defaultGoalId: string; selfId: () => string },
): void {
  pi.registerCommand("set-goal", {
    description: "Move a goal's acceptance criterion via the goals convention: /set-goal [goalId] <criterionId> <status> [headline]",
    handler: async (args: string, ctx: ExtensionCommandContext) => {
      const client = opts.getClient();
      if (!client) {
        ctx.ui.notify("sextant: not connected to the bus (no scoped creds, or reconnecting); cannot set a goal now", "error");
        return;
      }

      const parsed = parseArgs(args, opts.defaultGoalId);
      if ("error" in parsed) {
        ctx.ui.notify(parsed.error, "error");
        return;
      }
      const { goalId, criterionId, status, headline } = parsed;

      try {
        // setCriterion takes the Ops seam (the Client satisfies it) and `now`.
        const changed = await setCriterion(
          client,
          { goalId, criterionId, status, headline, by: opts.selfId() },
          new Date().toISOString(),
        );
        if (changed) {
          ctx.ui.notify(`goal ${goalId}: criterion "${criterionId}" → ${status} (announced on msg.topic.goals)`, "info");
        } else {
          ctx.ui.notify(`goal ${goalId}: criterion "${criterionId}" already ${status} (no change)`, "info");
        }
      } catch (e) {
        if (e instanceof SetCriterionError) {
          ctx.ui.notify(`set-goal failed at the ${e.step} step: ${e.message}`, "error");
        } else {
          ctx.ui.notify(`set-goal failed: ${(e as Error).message}`, "error");
        }
      }
    },
  });
}

// Parsed is the resolved /set-goal arguments.
interface Parsed {
  goalId: string;
  criterionId: string;
  status: string;
  headline: string;
}

// parseArgs reads the command line into a Parsed, or returns an error string.
// With a default goal configured, the leading goalId is optional: 3+ tokens with
// a valid status in position 2 means [goalId crit status ...]; otherwise it is
// [crit status ...] against the default goal. Exported for unit testing.
export function parseArgs(args: string, defaultGoalId: string): Parsed | { error: string } {
  const tokens = args.trim().split(/\s+/).filter((t) => t.length > 0);
  if (tokens.length < 2) {
    return { error: "usage: /set-goal [goalId] <criterionId> <status> [headline]" };
  }

  // Decide whether the first token is a goalId. If token[2] is a valid status,
  // the line is [goalId crit status ...]. If token[1] is a valid status, it is
  // [crit status ...] against the default goal.
  let goalId: string;
  let rest: string[];
  if (tokens.length >= 3 && VALID_STATUS.has(tokens[2]!)) {
    goalId = tokens[0]!;
    rest = tokens.slice(1);
  } else if (VALID_STATUS.has(tokens[1]!)) {
    if (!defaultGoalId) {
      return { error: "no default goal configured (set SEXTANT_GOAL_ID) — pass the goalId: /set-goal <goalId> <criterionId> <status> [headline]" };
    }
    goalId = defaultGoalId;
    rest = tokens;
  } else {
    return {
      error: `status must be one of: ${[...VALID_STATUS].join(", ")}`,
    };
  }

  const criterionId = rest[0]!;
  const status = rest[1]!;
  if (!VALID_STATUS.has(status)) {
    return { error: `status must be one of: ${[...VALID_STATUS].join(", ")}` };
  }
  const headline = rest.slice(2).join(" ") || `criterion ${criterionId} → ${status}`;
  return { goalId, criterionId, status, headline };
}
