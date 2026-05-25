/**
 * Unit tests for the sidecar lifecycle envelope publisher.
 *
 * Regresses plans/issues/bug-lifecycle-turn-ended-missing.md: the
 * wire-up acceptance requires `lifecycle.turn_ended` envelopes to be
 * published on `agents.<uuid>.lifecycle` at the end of every SDK turn.
 * The SDK driver (src/index.ts::newSDKDriver) already calls
 * `publishLifecycle("turn_ended")`; this test pins the envelope
 * contract — subject, kind, payload.transition, payload.state — so any
 * future refactor of the publisher (e.g. inlining or moving it back to
 * index.ts) can't accidentally drop the turn_ended emission without
 * the test catching it.
 */

import { describe, expect, it } from "vitest";
import {
  KIND_LIFECYCLE,
  type Client,
  type Envelope,
} from "@sextant/client";
import { publishLifecycle, type LifecycleEnv } from "../src/lifecycle.js";

interface CapturedPublish {
  subject: string;
  envelope: Envelope;
}

/**
 * Minimal `Client`-shaped stub that records every `.publish(subject,
 * envelope)` call. We only stub the methods `publishLifecycle` calls;
 * other Client methods throw if exercised so a test silently growing a
 * new dependency surfaces immediately.
 *
 * Cast through `unknown` because the real Client carries non-public
 * fields the type checker insists on. The tested function only uses
 * `publish`, so the cast is safe.
 */
function newCapturingClient(): {
  client: Client;
  captured: CapturedPublish[];
} {
  const captured: CapturedPublish[] = [];
  const stub = {
    publish: async (subject: string, envelope: Envelope): Promise<void> => {
      captured.push({ subject, envelope });
    },
  };
  return { client: stub as unknown as Client, captured };
}

const baseEnv: LifecycleEnv = {
  agentUuid: "11111111-2222-3333-4444-555555555555",
  hostId: "host-test",
};

describe("publishLifecycle", () => {
  it("emits lifecycle.turn_ended on the agent's lifecycle subject", async () => {
    const { client, captured } = newCapturingClient();
    await publishLifecycle(client, baseEnv, "incarnation-7", "turn_ended");

    expect(captured).toHaveLength(1);
    const [entry] = captured;
    expect(entry!.subject).toBe(`agents.${baseEnv.agentUuid}.lifecycle`);
    expect(entry!.envelope.kind).toBe(KIND_LIFECYCLE);
    expect(entry!.envelope.from).toMatchObject({
      kind: "agent",
      id: baseEnv.agentUuid,
      host: baseEnv.hostId,
    });
    const payload = entry!.envelope.payload as Record<string, unknown>;
    expect(payload["transition"]).toBe("turn_ended");
    expect(payload["incarnation_id"]).toBe("incarnation-7");
    expect(payload["agent_uuid"]).toBe(baseEnv.agentUuid);
    // turn_ended doesn't move the IncarnationState; sidecar reports `running`.
    expect(payload["state"]).toBe("running");
    // No reason on a clean turn_ended.
    expect(payload["reason"]).toBeUndefined();
  });

  it("threads reason='error' onto a failed turn_ended envelope", async () => {
    const { client, captured } = newCapturingClient();
    await publishLifecycle(client, baseEnv, "incarnation-7", "turn_ended", "error");

    expect(captured).toHaveLength(1);
    const payload = captured[0]!.envelope.payload as Record<string, unknown>;
    expect(payload["transition"]).toBe("turn_ended");
    expect(payload["reason"]).toBe("error");
  });

  it("emits lifecycle.started with state=running", async () => {
    const { client, captured } = newCapturingClient();
    await publishLifecycle(client, baseEnv, "incarnation-7", "started");

    expect(captured).toHaveLength(1);
    const payload = captured[0]!.envelope.payload as Record<string, unknown>;
    expect(payload["transition"]).toBe("started");
    expect(payload["state"]).toBe("running");
  });

  it("emits lifecycle.ended with state=ended", async () => {
    const { client, captured } = newCapturingClient();
    await publishLifecycle(client, baseEnv, "incarnation-7", "ended", "signal:SIGTERM");

    expect(captured).toHaveLength(1);
    const payload = captured[0]!.envelope.payload as Record<string, unknown>;
    expect(payload["transition"]).toBe("ended");
    expect(payload["state"]).toBe("ended");
    expect(payload["reason"]).toBe("signal:SIGTERM");
  });
});
