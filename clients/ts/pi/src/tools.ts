// The bus tools the pi agent can call (AC#2). Each is a thin, LLM-callable
// wrapper over the SDK Client's primitive operations — publish / read / subscribe
// / unsubscribe / the artifact CRUD — so a pi agent participates on the bus the
// same way any client does. The tools are PRIMITIVES (ADR-0005): content is
// opaque; they take and return records as-is and bake in no lexicon (a domain
// verb like /set-goal is a convention layered on top, in goal_command.ts).
//
// Every tool resolves the live client at call time via getClient(), because the
// client is reopened on each session transition (the idempotent session_start) —
// a tool must reach the CURRENT client, never one captured at load time (the
// disposed-binding trap the spike calls out). A call with no live client returns
// a clear tool error rather than throwing into the agent loop.
//
// Subscriptions opened by sextant_subscribe register the same wake path as the
// configured watch topics, so a topic the agent subscribes to at runtime wakes it
// like the inbox does. unsubscribe stops that.

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import type { Client, JSONValue, Message, Subscription } from "@sextant/sdk";
import { topicSubject } from "@sextant/sdk";

// ToolDeps is what the tools need from the extension: the live client, the wake
// handler to attach to a runtime subscription, and a place to track those
// subscriptions so unsubscribe and session teardown can stop them.
export interface ToolDeps {
  getClient: () => Client | undefined;
  // onWake is the same buffering wake path the inbox + watch topics use, so a
  // runtime subscription behaves identically to a configured one.
  onWake: (m: Message) => void;
  // subscriptions tracks runtime subscriptions by topic so unsubscribe can find
  // and stop one, and session_shutdown can stop them all. Owned by the extension.
  subscriptions: Map<string, Subscription>;
}

// JSON_RECORD is the opaque record shape the publish/artifact tools accept. Any
// JSON object — content is opaque to the bus, so the schema does not constrain it.
const JSON_RECORD = Type.Record(Type.String(), Type.Unknown(), {
  description: "An opaque JSON record (a lexicon). Content is not interpreted by the bus.",
});

// ok / err build the AgentToolResult shape pi expects (content blocks + isError
// + details). These tools carry no structured details, so details is undefined.
function ok(text: string): { content: { type: "text"; text: string }[]; isError: false; details: undefined } {
  return { content: [{ type: "text", text }], isError: false, details: undefined };
}
function err(text: string): { content: { type: "text"; text: string }[]; isError: true; details: undefined } {
  return { content: [{ type: "text", text }], isError: true, details: undefined };
}

// requireClient resolves the live client or returns a tool error explaining the
// agent is not bus-connected (it can retry — the client reopens on transitions).
function requireClient(deps: ToolDeps): Client | { error: ReturnType<typeof err> } {
  const c = deps.getClient();
  if (!c) return { error: err("not connected to the sextant bus right now (the client may be reopening across a session transition); try again shortly") };
  return c;
}

// registerTools wires all the bus tools onto pi. Names are sextant_<verb> so they
// are unmistakable in a tool list and never collide with pi's built-ins.
export function registerTools(pi: ExtensionAPI, deps: ToolDeps): void {
  pi.registerTool({
    name: "sextant_publish",
    label: "Bus publish",
    description:
      "Publish a message to a sextant bus topic so other crew can see it. Use this to talk on a shared topic. To reply to whoever messaged you, prefer sextant_reply. `topic` is a plain topic name (e.g. \"crew\"); the bus maps it to msg.topic.<topic>. `record` is an opaque JSON object — for plain chat use {\"$type\":\"chat.message\",\"text\":\"...\"}.",
    parameters: Type.Object({
      topic: Type.String({ description: "Plain topic name, e.g. \"crew\" (mapped to msg.topic.<topic>)." }),
      record: JSON_RECORD,
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      try {
        await c.publish(topicSubject(params.topic), params.record as JSONValue);
        return ok(`published to msg.topic.${params.topic}`);
      } catch (e) {
        return err(`publish failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_reply",
    label: "Bus reply",
    description:
      "Send a direct message to a specific bus client by its id (e.g. reply to whoever just messaged you — their id is in the bus message you received). `record` is an opaque JSON object; for plain chat use {\"$type\":\"chat.message\",\"text\":\"...\"}.",
    parameters: Type.Object({
      to: Type.String({ description: "The recipient client id (a 26-char ULID)." }),
      record: JSON_RECORD,
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      try {
        // A DM lands on the recipient's inbox subject (msg.client.<id>).
        await c.publish(`msg.client.${params.to}`, params.record as JSONValue);
        return ok(`sent a direct message to ${params.to}`);
      } catch (e) {
        return err(`reply failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_read",
    label: "Bus read",
    description:
      "Read recent retained messages on a topic (catch up on what was said while you were busy, or recover anything a flood dropped). Returns the most recent `limit` messages with their author and record.",
    parameters: Type.Object({
      topic: Type.String({ description: "Plain topic name to read, e.g. \"crew\"." }),
      limit: Type.Optional(Type.Integer({ description: "Max messages to return (default 20).", minimum: 1, maximum: 200 })),
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      const limit = params.limit ?? 20;
      try {
        // Pull from the start, keep the last `limit` (the bus paginates by cursor;
        // for a tool, a single bounded fetch of the tail is what the agent wants).
        const { frames } = await c.fetchMessages(topicSubject(params.topic), 0, 1000);
        const tail = frames.slice(-limit);
        const lines = tail.map((f) => `${f.author}: ${JSON.stringify(f.record)}`);
        return ok(lines.length ? lines.join("\n") : `(no messages on ${params.topic})`);
      } catch (e) {
        return err(`read failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_subscribe",
    label: "Bus subscribe",
    description:
      "Subscribe to a bus topic so new messages on it WAKE you (a turn fires when one arrives), in addition to your inbox. Use this to start following a shared topic.",
    parameters: Type.Object({
      topic: Type.String({ description: "Plain topic name to follow, e.g. \"crew\"." }),
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      const subject = topicSubject(params.topic);
      if (deps.subscriptions.has(subject)) return ok(`already subscribed to ${params.topic}`);
      try {
        const sub = await c.subscribe(subject, deps.onWake);
        deps.subscriptions.set(subject, sub);
        return ok(`subscribed to msg.topic.${params.topic}; new messages will wake you`);
      } catch (e) {
        return err(`subscribe failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_unsubscribe",
    label: "Bus unsubscribe",
    description: "Stop following a topic you subscribed to (its messages will no longer wake you).",
    parameters: Type.Object({
      topic: Type.String({ description: "Plain topic name to stop following." }),
    }),
    async execute(_id, params) {
      const subject = topicSubject(params.topic);
      const sub = deps.subscriptions.get(subject);
      if (!sub) return ok(`not subscribed to ${params.topic}`);
      try {
        await sub.stop();
        deps.subscriptions.delete(subject);
        return ok(`unsubscribed from ${params.topic}`);
      } catch (e) {
        return err(`unsubscribe failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_artifact_get",
    label: "Artifact get",
    description:
      "Read a shared artifact (durable shared state) by name. Returns its record and revision. Use the revision when updating to avoid clobbering a concurrent write.",
    parameters: Type.Object({
      name: Type.String({ description: "Artifact name, e.g. \"goal.v0-6-0\"." }),
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      try {
        const a = await c.getArtifact(params.name);
        return ok(`revision ${a.revision}:\n${JSON.stringify(a.record, null, 2)}`);
      } catch (e) {
        return err(`get artifact failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_artifact_put",
    label: "Artifact put",
    description:
      "Create or update a shared artifact. Omit `expectedRev` to CREATE a new artifact; pass the revision from sextant_artifact_get to UPDATE one (compare-and-set — fails if someone else moved it). Content is an opaque JSON record.",
    parameters: Type.Object({
      name: Type.String({ description: "Artifact name." }),
      record: JSON_RECORD,
      expectedRev: Type.Optional(
        Type.Integer({ description: "The revision you read; required to update, omit to create.", minimum: 0 }),
      ),
    }),
    async execute(_id, params) {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      try {
        if (params.expectedRev === undefined) {
          const rev = await c.createArtifact(params.name, params.record as JSONValue);
          return ok(`created ${params.name} at revision ${rev}`);
        }
        const rev = await c.updateArtifact(params.name, params.record as JSONValue, params.expectedRev);
        return ok(`updated ${params.name} to revision ${rev}`);
      } catch (e) {
        return err(`put artifact failed: ${(e as Error).message}`);
      }
    },
  });

  pi.registerTool({
    name: "sextant_artifact_list",
    label: "Artifact list",
    description: "List the names of shared artifacts (discovery; does not return their contents).",
    parameters: Type.Object({}),
    async execute() {
      const c = requireClient(deps);
      if ("error" in c) return c.error;
      try {
        const infos = await c.listArtifacts();
        return ok(infos.length ? infos.map((i) => `${i.name} (rev ${i.revision})`).join("\n") : "(no artifacts)");
      } catch (e) {
        return err(`list artifacts failed: ${(e as Error).message}`);
      }
    },
  });
}
