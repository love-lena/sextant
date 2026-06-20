// The headless destructive-action gate (the spike's layered-security adjustment
// 4b). Bus-delivered content is an untrusted prompt-injection surface, and pi is
// NOT a sandbox — it runs with the user's permissions. A bus message that says
// "rm -rf /" enters pi as ordinary input and would otherwise flow straight to the
// bash tool. pi gives extensions a tool_call hook that can block; this module
// ships a sane, overridable default: when there is NO UI to confirm (headless,
// the unattended-worker case), destructive tool calls are BLOCKED by default.
//
// This is defense in depth, not the whole defense. The real isolation for an
// untrusted/unattended worker is the OS boundary (a container/VM, per pi's own
// security guidance — see the ADR). The gate raises the floor: a wandering or
// injected agent cannot quietly delete a tree or sudo something while no human is
// watching. With a UI present, the gate defers to pi's normal confirmation flow
// (the operator decides), so an interactive pi session is not hobbled.
//
// classifyDestructive is pure and unit-tested; registerGate wires it to pi.

import type { ExtensionAPI, ToolCallEvent, ToolCallEventResult } from "@earendil-works/pi-coding-agent";

// Destructive bash patterns: irreversible filesystem loss, privilege escalation,
// over-broad permission changes, and piping the network straight into a shell.
// Deliberately conservative — these are the actions that are unsafe to run
// unattended on an untrusted prompt, not a lint of bad shell style.
const DESTRUCTIVE_BASH = [
  /\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r|--recursive|--force)/i, // rm -rf / rm -fr / rm --recursive
  /\bsudo\b/i, // privilege escalation
  /\b(chmod|chown)\b[^\n]*\b777\b/i, // world-writable
  /\bmkfs\b|\bdd\s+[^\n]*\bof=\/dev\//i, // format / raw-device write
  /\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|fi|da)?sh\b/i, // curl ... | sh (remote-exec)
  /\bgit\s+push\b[^\n]*--force(?!-with-lease)/i, // force-push (not --force-with-lease)
  /:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;/, // fork bomb
];

// Built-in pi tools that mutate the filesystem. A bus-driven write/edit while
// unattended is the same class of risk as a destructive bash command, so the
// headless gate covers them too. (read / grep / find / ls are read-only and pass.)
const MUTATING_TOOLS = new Set(["write", "edit"]);

// classifyDestructive reports whether a tool call is destructive enough to block
// when headless. It is conservative and explainable — it returns a reason so the
// block, and the dash, can say WHY. A non-destructive call returns
// { destructive: false }.
export function classifyDestructive(toolName: string, input: Record<string, unknown>): {
  destructive: boolean;
  reason?: string;
} {
  if (toolName === "bash") {
    const command = typeof input["command"] === "string" ? input["command"] : "";
    for (const p of DESTRUCTIVE_BASH) {
      if (p.test(command)) {
        return { destructive: true, reason: `destructive bash command (matched ${p})` };
      }
    }
    return { destructive: false };
  }
  if (MUTATING_TOOLS.has(toolName)) {
    return { destructive: true, reason: `filesystem-mutating tool "${toolName}"` };
  }
  return { destructive: false };
}

// registerGate installs the headless block-by-default tool_call gate. When
// enabled and there is no UI, a destructive tool call is blocked with an
// explanatory reason; otherwise it passes (a UI-equipped pi defers to the
// operator's own confirmation). onBlock is an optional hook the extension uses to
// surface a block onto the activity bridge, so a blocked action is visible in the
// dash rather than silent. enabled is the config switch (default on, overridable).
export function registerGate(
  pi: ExtensionAPI,
  opts: {
    enabled: boolean;
    onBlock?: (toolName: string, reason: string) => void;
  },
): void {
  pi.on("tool_call", (event: ToolCallEvent, ctx): ToolCallEventResult | undefined => {
    if (!opts.enabled) return undefined;
    // A UI is present → pi's normal confirmation flow owns the decision; don't
    // double-gate an interactive operator.
    if (ctx.hasUI) return undefined;

    const verdict = classifyDestructive(event.toolName, event.input as Record<string, unknown>);
    if (!verdict.destructive) return undefined;

    const reason = `sextant-pi headless gate: ${verdict.reason ?? "destructive action"} blocked (no UI to confirm; run unattended/untrusted pi in a container/VM, or set SEXTANT_PI_GATE_HEADLESS=off to disable this gate)`;
    opts.onBlock?.(event.toolName, reason);
    return { block: true, reason };
  });
}
