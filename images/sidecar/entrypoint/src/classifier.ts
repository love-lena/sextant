/**
 * Tool-use classifier for the sidecar SDK driver.
 *
 * Implements the same semantics as Claude Code CLI's `--permission-mode auto`:
 * allow file-edit tools and safe bash commands; deny the obvious bright-line
 * destructive patterns; allow MCP tools (gated server-side by JWT).
 *
 * Spec: plans/issues/bug-sidecar-bash-still-asks-in-acceptedits.md §"Option A"
 */

/**
 * File-edit tools the SDK's `acceptEdits` permissionMode already auto-allows.
 * Listed explicitly so `classifyTool` is self-contained and testable without
 * the SDK wired up.
 */
export const SAFE_TOOLS = new Set([
  "Edit",
  "Write",
  "MultiEdit",
  "Read",
  "Glob",
  "Grep",
  "TodoWrite",
  "NotebookEdit",
]);

/**
 * Bright-line dangerous bash patterns.
 *
 * Policy: permissive by default, only block obvious destructive footguns.
 * Mirrors the intent of `--permission-mode auto`; does NOT try to be a
 * comprehensive sandbox — that is the container boundary's job.
 *
 * Each entry is either:
 *   { kind: "contains"; pattern: string }  — substring match (case-sensitive)
 *   { kind: "regex";    pattern: RegExp }  — regex match
 */
type DenyPattern =
  | { kind: "contains"; pattern: string; label: string }
  | { kind: "regex"; pattern: RegExp; label: string };

const DENY_PATTERNS: DenyPattern[] = [
  // sudo — check first so "sudo rm -rf /etc" labels as sudo, not rm-rf-root.
  // Containers don't need sudo for in-workspace work; it also signals an
  // attempt to escalate beyond the container's user.
  { kind: "regex", pattern: /\bsudo\s/, label: "sudo" },
  // Workspace nuke — check before the generic root pattern because
  // "/workspace" contains "/", and more-specific match is more informative.
  { kind: "contains", pattern: "rm -rf /workspace", label: "rm-rf-workspace" },
  // rm -rf home — check before root for the same reason
  { kind: "contains", pattern: "rm -rf ~/", label: "rm-rf-home" },
  { kind: "contains", pattern: "rm -rf ~", label: "rm-rf-home-bare" },
  // rm -rf root — broad catch after the specific cases above
  { kind: "contains", pattern: "rm -rf /", label: "rm-rf-root" },
  // Disk wipe patterns
  { kind: "contains", pattern: "dd if=/dev/zero", label: "dd-zero" },
  { kind: "contains", pattern: "dd if=/dev/random", label: "dd-random" },
  // mkfs — would format a block device
  { kind: "regex", pattern: /\bmkfs\./, label: "mkfs" },
  // Fork bomb
  { kind: "contains", pattern: ":(){:|:&};:", label: "fork-bomb" },
  // curl/wget piped to shell — remote code execution via pipe
  // (the M3 ClickHouse incident pattern; agents needing binary installs
  //  should ask the operator instead of self-installing)
  {
    kind: "regex",
    pattern: /\b(curl|wget)\b[^|]*\|\s*(bash|sh)\b/,
    label: "curl-pipe-shell",
  },
];

/**
 * Returns the label of the first matching deny pattern, or `undefined` if the
 * command is safe to run.
 */
export function isDangerousBashCommand(cmd: string): string | undefined {
  for (const entry of DENY_PATTERNS) {
    const matched =
      entry.kind === "contains"
        ? cmd.includes(entry.pattern)
        : entry.pattern.test(cmd);
    if (matched) return entry.label;
  }
  return undefined;
}

/** Result returned by `classifyTool`. */
export type ToolDecision =
  | { behavior: "allow"; updatedInput: Record<string, unknown> }
  | { behavior: "deny"; message: string };

/**
 * Classify a tool invocation.
 *
 * - Safe file-edit tools → allow unconditionally.
 * - Bash → allow unless `isDangerousBashCommand` fires.
 * - `mcp__*` → allow (gated server-side by JWT).
 * - Everything else → deny (unknown tool; fail safe).
 */
export function classifyTool(
  toolName: string,
  input: Record<string, unknown>,
): ToolDecision {
  if (SAFE_TOOLS.has(toolName)) {
    return { behavior: "allow", updatedInput: input };
  }

  if (toolName === "Bash") {
    const cmd = typeof input["command"] === "string" ? input["command"] : "";
    const denyLabel = isDangerousBashCommand(cmd);
    if (denyLabel !== undefined) {
      return {
        behavior: "deny",
        message: `dangerous bash refused (${denyLabel}): ${cmd}`,
      };
    }
    return { behavior: "allow", updatedInput: input };
  }

  if (toolName.startsWith("mcp__")) {
    return { behavior: "allow", updatedInput: input };
  }

  // Unknown tool — fail safe.
  return { behavior: "deny", message: `unknown tool: ${toolName}` };
}
