/**
 * Lifecycle envelope publisher.
 *
 * Extracted from `src/index.ts` so the test suite can exercise the
 * envelope contract — notably `transition=turn_ended`, which the SDK
 * driver publishes at the end of every prompt — without importing
 * `index.ts` (which kicks off `main()` and reads env vars at import
 * time).
 *
 * Regresses plans/issues/bug-lifecycle-turn-ended-missing.md.
 */

import {
  ADDRESS_AGENT,
  KIND_LIFECYCLE,
  newEnvelope,
  type Client,
} from "@sextant/client";

/** Minimal env subset the lifecycle publisher needs. */
export interface LifecycleEnv {
  agentUuid: string;
  hostId: string;
}

/**
 * Lifecycle transitions emitted by the sidecar. Mirrors
 * `pkg/sextantproto.LifecycleEvent`; `turn_ended` is the per-prompt
 * marker that wire-up dispatch acceptance requires.
 */
export type LifecycleTransition = "started" | "ended" | "turn_ended";

/**
 * State to advertise alongside a transition. The sidecar reports
 * `running` for both `started` and `turn_ended` (the agent is still
 * alive after a turn), and `ended` for `ended`.
 */
function stateForTransition(t: LifecycleTransition): string {
  switch (t) {
    case "started":
      return "running";
    case "ended":
      return "ended";
    default:
      // turn_ended doesn't move the IncarnationState; report current.
      return "running";
  }
}

/**
 * Publish one lifecycle envelope on `agents.<uuid>.lifecycle`.
 *
 * The envelope payload mirrors `pkg/sextantproto.LifecyclePayload`:
 * incarnation_id + agent_uuid + transition + state, with an optional
 * reason (used to flag `transition=turn_ended reason=error`).
 */
export async function publishLifecycle(
  client: Client,
  env: LifecycleEnv,
  incarnationId: string,
  transition: LifecycleTransition,
  reason?: string,
): Promise<void> {
  const payload = {
    incarnation_id: incarnationId,
    agent_uuid: env.agentUuid,
    transition,
    state: stateForTransition(transition),
    ...(reason ? { reason } : {}),
  };
  const envelope = newEnvelope(
    KIND_LIFECYCLE,
    { kind: ADDRESS_AGENT, id: env.agentUuid, host: env.hostId },
    payload,
  );
  await client.publish(`agents.${env.agentUuid}.lifecycle`, envelope);
}
