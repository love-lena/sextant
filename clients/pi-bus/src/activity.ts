// The agent.activity observability bridge (the spike's adjustment 3 — first-class,
// not a debug aid). It maps pi's own event stream (turns, thinking, the assistant
// reply, tool calls) into agent.activity records and publishes them on the agent's
// per-agent activity subject, so a dash or crew client reading it renders a headless
// worker like any other crew member without attaching to its terminal (TASK-150/151).
// pi is the first producer of this harness-neutral shape; other harnesses emit the
// identical record on the same subject (the TASK-151 adapter seam).
//
// The record shape is the agent.activity lexicon (protocol/lexicons/agent.activity.json):
// a small, fixed vocabulary — kind, turnIndex, the tool name/args/result, the
// thinking/reply text — TRUNCATED to a preview, because the bus record is a signal
// for the dash, not the durable log (the worker's own session JSONL keeps the
// full detail). The bus stamps the author, so the record never claims one.
//
// The publish path is a small seam (Publisher) so the bridge is unit-testable
// against a fake without the SDK or a bus, and the extension wires the live
// client in. Publishing is best-effort and fire-and-forget: an activity publish
// failure must never disturb the agent's turn.

import type { AgentEndEvent, TurnEndEvent, TurnStartEvent } from "@earendil-works/pi-coding-agent";
import type { JSONValue } from "@sextant/sdk";

// The tool-execution event shapes pi delivers to tool_execution_start /
// tool_execution_end handlers. pi does not re-export these event types from its
// public index (only the turn/agent ones), so we declare the minimal structural
// subset the bridge reads — the same fields the spike consumed.
export interface ToolExecutionStartEvent {
  toolCallId: string;
  toolName: string;
  args: unknown;
}
export interface ToolExecutionEndEvent {
  toolCallId: string;
  toolName: string;
  result: unknown;
  isError: boolean;
}

// Publisher is the one bus operation the bridge needs: publish a record to a
// subject. The SDK's Client.publish satisfies it; a test fake records calls.
export interface Publisher {
  publish(subject: string, record: JSONValue): Promise<void>;
}

// ActivityRecord is the agent.activity lexicon record (the fields the dash reads).
// Optional fields are omitted when empty so the published record is minimal and
// canonicalizes predictably.
export interface ActivityRecord {
  $type: "agent.activity";
  kind: "turn_start" | "turn_end" | "tool_start" | "tool_end" | "thinking" | "message";
  turnIndex?: number;
  tool?: string;
  toolCallId?: string;
  args?: string;
  result?: string;
  isError?: boolean;
  text?: string;
  updated?: string;
}

// ActivityBridge turns pi events into agent.activity publishes on the per-agent
// activity subject. It owns NO pi/bus wiring — index.ts subscribes the pi events
// and supplies the live publisher + subject mapper. previewMax bounds every text field.
export class ActivityBridge {
  constructor(
    private readonly opts: {
      publisher: () => Publisher | undefined; // resolved at publish time (client may be reopening)
      topicSubject: () => string; // the bus subject to publish on (msg.agent.<id>.activity)
      previewMax: number;
      onError?: (e: Error) => void;
      now?: () => Date;
    },
  ) {}

  onTurnStart(e: TurnStartEvent): void {
    this.emit({ $type: "agent.activity", kind: "turn_start", turnIndex: e.turnIndex });
  }

  // onTurnEnd emits the turn marker AND, if the assistant message carried any,
  // the thinking and the reply text as their own records — so the dash shows a
  // worker's reasoning and answer, not just that a turn happened.
  onTurnEnd(e: TurnEndEvent): void {
    const { thinking, text } = extractText(e.message);
    if (thinking) {
      this.emit({ $type: "agent.activity", kind: "thinking", turnIndex: e.turnIndex, text: this.preview(thinking) });
    }
    if (text) {
      this.emit({ $type: "agent.activity", kind: "message", turnIndex: e.turnIndex, text: this.preview(text) });
    }
    this.emit({ $type: "agent.activity", kind: "turn_end", turnIndex: e.turnIndex });
  }

  // onAgentEnd is a fallback emitter for the final assistant text in modes where
  // turn_end's message is sparse: index.ts uses turn_end as the primary source
  // and may skip this. Kept for symmetry; emits nothing on its own.
  onAgentEnd(_e: AgentEndEvent): void {
    /* primary text path is turn_end; agent_end carries no per-turn index */
  }

  onToolStart(e: ToolExecutionStartEvent): void {
    this.emit({
      $type: "agent.activity",
      kind: "tool_start",
      tool: e.toolName,
      toolCallId: e.toolCallId,
      args: this.preview(renderArgs(e.args)),
    });
  }

  onToolEnd(e: ToolExecutionEndEvent): void {
    this.emit({
      $type: "agent.activity",
      kind: "tool_end",
      tool: e.toolName,
      toolCallId: e.toolCallId,
      isError: e.isError,
      result: this.preview(renderResult(e.result)),
    });
  }

  // emitRaw publishes an arbitrary activity record (used by the gate-block hook
  // to surface a blocked tool call as a tool_end with isError).
  emitRaw(rec: ActivityRecord): void {
    this.emit(rec);
  }

  private emit(rec: ActivityRecord): void {
    const pub = this.opts.publisher();
    if (!pub) return; // no live client (reopening / not yet connected) — drop, best-effort
    const stamped: ActivityRecord = { ...rec, updated: (this.opts.now?.() ?? new Date()).toISOString() };
    void pub.publish(this.opts.topicSubject(), stamped as unknown as JSONValue).catch((e) => {
      this.opts.onError?.(e as Error);
    });
  }

  private preview(s: string): string {
    if (s.length <= this.opts.previewMax) return s;
    return s.slice(0, this.opts.previewMax) + "…";
  }
}

// extractText pulls the thinking and the assistant reply out of an assistant
// message's content blocks. pi's AssistantMessage content is an array of
// { type:"text"|"thinking"|... } blocks (pi-ai); a custom/user message has a
// string or different shape, so this tolerates anything and returns "" for the
// parts it cannot find.
export function extractText(message: unknown): { thinking: string; text: string } {
  const content = (message as { content?: unknown })?.content;
  if (!Array.isArray(content)) {
    // A plain string content (some message roles) is the reply text.
    if (typeof content === "string") return { thinking: "", text: content };
    return { thinking: "", text: "" };
  }
  const thinking: string[] = [];
  const text: string[] = [];
  for (const block of content) {
    if (block === null || typeof block !== "object") continue;
    const b = block as { type?: unknown; text?: unknown; thinking?: unknown };
    if (b.type === "thinking" && typeof b.thinking === "string") thinking.push(b.thinking);
    else if (b.type === "text" && typeof b.text === "string") text.push(b.text);
  }
  return { thinking: thinking.join("\n"), text: text.join("\n") };
}

// renderArgs renders tool arguments to a short human string for the dash. The
// bash command is the salient field; otherwise a compact JSON of the args.
function renderArgs(args: unknown): string {
  if (args && typeof args === "object" && !Array.isArray(args)) {
    const a = args as Record<string, unknown>;
    if (typeof a["command"] === "string") return a["command"];
    if (typeof a["path"] === "string") return String(a["path"]);
  }
  try {
    return JSON.stringify(args);
  } catch {
    return String(args);
  }
}

// renderResult renders a tool result to a short string. pi tool results are
// typically { content: [{type:"text", text}] } or a plain value; tolerate both.
function renderResult(result: unknown): string {
  if (typeof result === "string") return result;
  if (result && typeof result === "object") {
    const r = result as { content?: unknown; output?: unknown };
    if (typeof r.output === "string") return r.output;
    if (Array.isArray(r.content)) {
      const parts: string[] = [];
      for (const c of r.content) {
        if (c && typeof c === "object" && typeof (c as { text?: unknown }).text === "string") {
          parts.push((c as { text: string }).text);
        }
      }
      if (parts.length > 0) return parts.join("\n");
    }
  }
  try {
    return JSON.stringify(result);
  } catch {
    return String(result);
  }
}
