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

// `rm -<flags>` where the flag block contains both `r` and `f` (any order,
// possibly with other letters like `v`). Used as the leading fragment of the
// anchored rm-rf path regexes below. The double lookahead enforces "has r"
// AND "has f" without forcing either order, so `-rf`, `-fr`, `-rfv`, `-fvr`,
// `-rvf`, `-vrf` all match while a benign `-v` alone does not.
//
// Reference: plans/issues/bug-classifier-rm-rf-too-broad.md
const RM_RF_FLAGS = String.raw`\brm\s+-(?=[a-zA-Z]*r)(?=[a-zA-Z]*f)[a-zA-Z]+\s+`;

const DENY_PATTERNS: DenyPattern[] = [
  // sudo — check first so "sudo rm -rf /etc" labels as sudo, not rm-rf-root.
  // Containers don't need sudo for in-workspace work; it also signals an
  // attempt to escalate beyond the container's user.
  { kind: "regex", pattern: /\bsudo\s/, label: "sudo" },
  // Workspace nuke — check before the generic root pattern because
  // "/workspace" contains "/", and more-specific match is more informative.
  // Anchored: only fires on `rm -rf /workspace` or `/workspace/...`, not on
  // any command that happens to contain the substring.
  {
    kind: "regex",
    pattern: new RegExp(RM_RF_FLAGS + String.raw`\/workspace(\s|\/|$)`),
    label: "rm-rf-workspace",
  },
  // rm -rf home — `~`, `~/...`, `$HOME`, `$HOME/...`. Check before root.
  {
    kind: "regex",
    pattern: new RegExp(RM_RF_FLAGS + String.raw`(~|\$HOME)(\s|\/|$)`),
    label: "rm-rf-home",
  },
  // rm -rf root — `rm -rf /` or `rm -rf / <something>`. Anchored so that
  // benign paths like `rm -rf /tmp/foo` or `rm -rf node_modules` fall
  // through to allow. The trailing `(\s|$)` requires a space or end after
  // the `/`, so `rm -rf /tmp` (which has `t` after `/`) does not match.
  {
    kind: "regex",
    pattern: new RegExp(RM_RF_FLAGS + String.raw`\/(\s|$)`),
    label: "rm-rf-root",
  },
  // Disk wipe patterns
  { kind: "contains", pattern: "dd if=/dev/zero", label: "dd-zero" },
  { kind: "contains", pattern: "dd if=/dev/random", label: "dd-random" },
  // mkfs — would format a block device
  { kind: "regex", pattern: /\bmkfs\./, label: "mkfs" },
  // Fork bomb
  { kind: "contains", pattern: ":(){:|:&};:", label: "fork-bomb" },
  // curl/wget piped to shell — remote code execution via pipe
  // (the M3 ClickHouse incident pattern; agents needing binary installs
  //  should ask the operator instead of self-installing).
  //
  // `.*` (not `[^|]*`) so intermediate pipes like
  //   `curl X | tee /tmp/log | bash`
  // still match. Reference:
  // plans/issues/bug-classifier-curl-multipipe-bypass.md
  {
    kind: "regex",
    pattern: /\b(curl|wget)\b.*\|\s*(bash|sh)\b/,
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
