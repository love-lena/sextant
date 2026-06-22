// Configuration for the @sextant/pi-bus extension. Read once at session_start
// from the environment, with sensible, overridable defaults (the standing
// "ship defaults + override hatches" discipline). A pi extension has no settings
// file of its own, so the environment is the natural surface — the same one the
// TASK-176 spike used, so its driver wires a fresh bus per run without ceremony.
//
// The one REQUIRED value is SEXTANT_PI_CREDS: this agent's OWN scoped credential
// (ADR-0020, ADR-0041). The extension acts on that identity and never the
// operator's ambient context — a co-equal crew member, not an impersonator. The
// bus is discovered from SEXTANT_BUS_URL (a NATS URL, wins) or SEXTANT_BUS_JSON
// (a bus.json discovery file), matching the SDK's resolution order.

// Defaults. Each is overridable by the matching env var.
//
// MAX_BUFFERED bounds the per-source inbound queue (back-pressure, the spike's
// AC#3). Small on purpose: a wake is "come look at the bus", not at-least-once
// delivery — the durable record lives on the bus, so dropping the oldest queued
// wake under flood loses no content (the agent can read the topic to recover).
const DEFAULT_MAX_BUFFERED = 16;

// COALESCE_WINDOW_MS groups a burst from one author on one topic into a single
// "N new on <topic>" wake instead of N separate turns. 0 disables coalescing.
const DEFAULT_COALESCE_WINDOW_MS = 1500;

// PREVIEW_MAX caps how much of a tool's args/result/text the pi.activity bridge
// puts on the bus. The bus record is a signal for the dash, not the durable log;
// the worker's own session JSONL keeps the full detail.
const DEFAULT_PREVIEW_MAX = 600;

// HANDOFF_TOPIC is where the managed handoff (TASK-178) announces relinquished /
// acquired, so a dash + the dispatcher see ownership move. A drain REQUEST is a DM
// to the worker's inbox; only the announcements go here.
const DEFAULT_HANDOFF_TOPIC = "pi.handoff";

// Config is the resolved extension configuration for one session.
export interface Config {
  // credsPath is this agent's OWN scoped credential (REQUIRED). "" means the
  // extension cannot open a client and stays dormant (it logs and no-ops).
  credsPath: string;
  // busURL is the NATS URL to dial; "" falls back to busJSONPath.
  busURL: string;
  // busJSONPath is a bus.json discovery file used when busURL is empty.
  busJSONPath: string;
  // watchTopics are extra topics (besides the inbox) the agent subscribes to and
  // wakes on. Plain topic names (e.g. "crew"), mapped to msg.topic.<name>.
  watchTopics: string[];
  // activityTopic is the topic the pi.activity bridge publishes to. Defaults to
  // pi.activity.<agent-id> when empty, so each worker has its own stream the dash
  // can render independently (TASK-150/151).
  activityTopic: string;
  // goalId is the goal /set-goal operates on when the command is invoked without
  // an explicit goal id. "" means /set-goal requires the id as an argument.
  goalId: string;
  // maxBuffered bounds the inbound back-pressure queue.
  maxBuffered: number;
  // coalesceWindowMs groups a same-author/same-topic burst into one wake. 0 off.
  coalesceWindowMs: number;
  // previewMax caps the activity-bridge arg/result/text preview length.
  previewMax: number;
  // gateDestructiveHeadless blocks destructive tool calls by default when there
  // is no UI to confirm (the spike's layered-security adjustment). Default true;
  // set SEXTANT_PI_GATE_HEADLESS=off to run a trusted unattended worker without
  // it (e.g. inside a container/VM, the real isolation boundary).
  gateDestructiveHeadless: boolean;
  // handoffTopic is the topic the managed close-and-resume handoff (TASK-178)
  // announces relinquished/acquired on, so the dash + the dispatcher see the
  // ownership transfer. A drain REQUEST always arrives on the worker's inbox (a
  // pi.handoff DM); this is only where the worker's ANNOUNCEMENTS go. Defaults to
  // the shared "pi.handoff" topic.
  handoffTopic: string;
  // drainWhenIdle makes the worker DRAIN AND EXIT on its own once a turn finishes
  // and there is nothing left to do (idle + empty wake queue) — the drain-and-revive
  // model (ADR-0045): a dispatched worker does its task, reports, and exits, and the
  // dispatcher re-spawns it (resuming its session) on the next message. It reuses the
  // managed-handoff wind-down (relinquish → close → exit). Default OFF, so a bare
  // interactive `pi -e` session stays resident; the dispatcher recipe (pi.sh) opts in
  // by setting SEXTANT_PI_DRAIN_WHEN_IDLE=1.
  drainWhenIdle: boolean;
}

// resolveConfig reads the environment into a Config, applying the defaults above.
// It never throws: a missing credential yields credsPath:"" so the caller can
// log a clear config error and leave the agent running (just not bus-connected),
// rather than crashing a pi session over a misconfiguration.
export function resolveConfig(env: NodeJS.ProcessEnv): Config {
  return {
    credsPath: env["SEXTANT_PI_CREDS"] ?? "",
    busURL: env["SEXTANT_BUS_URL"] ?? "",
    busJSONPath: env["SEXTANT_BUS_JSON"] ?? "",
    watchTopics: splitTopics(env["SEXTANT_WATCH_TOPICS"] ?? env["SEXTANT_WATCH_TOPIC"] ?? ""),
    activityTopic: env["SEXTANT_ACTIVITY_TOPIC"] ?? "",
    goalId: env["SEXTANT_GOAL_ID"] ?? "",
    maxBuffered: posInt(env["SEXTANT_PI_MAX_BUFFERED"], DEFAULT_MAX_BUFFERED),
    coalesceWindowMs: nonNegInt(env["SEXTANT_PI_COALESCE_MS"], DEFAULT_COALESCE_WINDOW_MS),
    previewMax: posInt(env["SEXTANT_PI_PREVIEW_MAX"], DEFAULT_PREVIEW_MAX),
    gateDestructiveHeadless: !isOff(env["SEXTANT_PI_GATE_HEADLESS"]),
    handoffTopic: env["SEXTANT_PI_HANDOFF_TOPIC"] || DEFAULT_HANDOFF_TOPIC,
    drainWhenIdle: isOn(env["SEXTANT_PI_DRAIN_WHEN_IDLE"]),
  };
}

// defaultActivityTopic is pi.activity.<id> — the per-worker activity stream when
// no explicit topic is configured.
export function defaultActivityTopic(agentId: string): string {
  return "pi.activity." + agentId;
}

// splitTopics parses a comma/space-separated topic list, dropping empties.
function splitTopics(s: string): string[] {
  return s
    .split(/[,\s]+/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0);
}

function posInt(s: string | undefined, fallback: number): number {
  const n = s === undefined ? NaN : Number(s);
  return Number.isInteger(n) && n > 0 ? n : fallback;
}

function nonNegInt(s: string | undefined, fallback: number): number {
  const n = s === undefined ? NaN : Number(s);
  return Number.isInteger(n) && n >= 0 ? n : fallback;
}

// isOff reads a boolean-ish "turn it off" env value: off / false / 0 / no.
function isOff(s: string | undefined): boolean {
  if (s === undefined) return false;
  const v = s.trim().toLowerCase();
  return v === "off" || v === "false" || v === "0" || v === "no";
}

// isOn reads a boolean-ish "turn it on" env value: on / true / 1 / yes. Unset is off.
function isOn(s: string | undefined): boolean {
  if (s === undefined) return false;
  const v = s.trim().toLowerCase();
  return v === "on" || v === "true" || v === "1" || v === "yes";
}
