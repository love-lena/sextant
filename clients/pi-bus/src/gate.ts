// The headless tool gate — the pi worker's in-process command + confinement
// guard (TASK-118; grew from the spike's layered-security adjustment 4b). Bus-
// delivered content is an untrusted prompt-injection surface, and pi is NOT a
// sandbox — it runs with the user's permissions. A bus message that says
// "rm -rf /" or "read ~/Documents/secrets" enters pi as ordinary input and would
// otherwise flow straight to the bash/read tool. pi gives extensions a tool_call
// hook that can block; this module is where the worker's autonomy boundary is
// shell-enforced (the sibling of the wf-release-pr release-path wrapper).
//
// Two layers, both enforcement (not playbook compliance):
//
//   1. DESTRUCTIVE / GUI / SYSTEM command deny (classifyDestructive). The classic
//      irreversible-loss + privilege-escalation set, PLUS the GUI/system blast-
//      radius classes that made a dispatched worker dangerous on the operator's
//      machine (killall/pkill, osascript, `open`, package installs, shutdown —
//      the Firefox-close scare). Blocked when headless (no UI to confirm); an
//      interactive pi defers to its own confirmation flow. Overridable via
//      SEXTANT_PI_GATE_HEADLESS=off (the trusted-unattended hatch, e.g. in a VM).
//
//   2. WORKDIR CONFINEMENT (classifyConfinement). When the recipe sets a scoped
//      working dir (SEXTANT_PI_WORKDIR — pi.sh always does for a dispatched
//      worker), a file tool (read/write/edit/grep/find/ls) or a bash command that
//      reaches OUTSIDE that dir is BLOCKED. This is the enforced half of AC#1: a
//      worker TOLD to read ~/Documents still cannot, because the gate denies the
//      escaping path — it does not rely on the worker not trying. Confinement is
//      INDEPENDENT of the destructive toggle: turning the destructive gate off
//      (a trusted worker that may run privileged commands in its tree) does NOT
//      unscope it from its directory. It is disabled only when no workdir is set
//      (a bare interactive `pi -e` session, where there is no scope to enforce).
//
// This raises the floor; it is not the whole defense — the real isolation for an
// untrusted unattended agent is the OS boundary (a container/VM, per pi's own
// guidance). classifyDestructive + classifyConfinement are pure and unit-tested;
// registerGate wires them to pi.

import { isAbsolute, normalize, resolve, sep } from "node:path";
import type { ExtensionAPI, ToolCallEvent, ToolCallEventResult } from "@earendil-works/pi-coding-agent";

// Destructive bash patterns: irreversible filesystem loss, privilege escalation,
// over-broad permission changes, piping the network into a shell — AND the
// GUI/system command classes a dispatched worker has no business running (they
// reach the operator's apps + machine, not the worker's task). Deliberately
// conservative — these are unsafe to run unattended on an untrusted prompt.
const DESTRUCTIVE_BASH = [
  /\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r|--recursive|--force)/i, // rm -rf / rm -fr / rm --recursive
  /\bsudo\b/i, // privilege escalation
  /\b(chmod|chown)\b[^\n]*\b777\b/i, // world-writable
  /\bmkfs\b|\bdd\s+[^\n]*\bof=\/dev\//i, // format / raw-device write
  /\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|fi|da)?sh\b/i, // curl ... | sh (remote-exec)
  /\bgit\s+push\b[^\n]*--force(?!-with-lease)/i, // force-push (not --force-with-lease)
  /:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;/, // fork bomb
  // GUI / system blast-radius classes (TASK-118): process-kill, AppleScript,
  // the macOS app/file launcher, package installers, and machine power control.
  /\b(killall|pkill)\b/i, // kill processes by name (the Firefox-close vector)
  /\bosascript\b/i, // drive the GUI / other apps via AppleScript
  /\bopen\s+(-a\b|-b\b|[^\n]*\.app\b)/i, // `open -a Firefox` / open an .app
  /\b(brew|port|apt(-get)?|yum|dnf|pacman|npm|pnpm|yarn|pip3?|gem|cargo|go)\s+(install|add|i)\b/i, // package installs
  /\b(shutdown|reboot|halt|poweroff)\b/i, // machine power control
];

// Built-in pi tools that mutate the filesystem. A bus-driven write/edit while
// unattended is the same class of risk as a destructive bash command, so the
// headless gate covers them too. (read / grep / find / ls are read-only and pass
// the DESTRUCTIVE check — but they are still subject to CONFINEMENT below.)
const MUTATING_TOOLS = new Set(["write", "edit"]);

// File tools whose path input must stay inside the scoped workdir. Covers both
// the read-only (read/grep/find/ls) and mutating (write/edit) built-ins: AC#1 is
// about TRAVERSAL too ("cannot read or traverse the operator's home"), so a read
// outside the scope is blocked, not just a write.
const PATH_TOOLS = new Set(["read", "write", "edit", "grep", "find", "ls"]);

// classifyDestructive reports whether a tool call is destructive enough to block
// when headless. It is conservative and explainable — it returns a reason so the
// block, and the dash, can say WHY. A non-destructive call returns
// { destructive: false }.
export function classifyDestructive(
  toolName: string,
  input: Record<string, unknown>,
): { destructive: boolean; reason?: string } {
  if (toolName === "bash") {
    const command = typeof input["command"] === "string" ? input["command"] : "";
    for (const p of DESTRUCTIVE_BASH) {
      if (p.test(command)) {
        return { destructive: true, reason: `destructive/GUI/system bash command (matched ${p})` };
      }
    }
    return { destructive: false };
  }
  if (MUTATING_TOOLS.has(toolName)) {
    return { destructive: true, reason: `filesystem-mutating tool "${toolName}"` };
  }
  return { destructive: false };
}

// candidatePaths pulls the filesystem path(s) a tool call would touch from its
// input. The built-in file tools name their target in a `path` field (and edit/
// write may also carry it as `file`/`filename`); a few accept a `paths` array.
// It is best-effort: a path it cannot see is simply not confined here (bash is
// handled separately, and the recipe's CWD + PATH guard backs it up), but every
// path it DOES see is checked, so the structured file tools are confined exactly.
function candidatePaths(input: Record<string, unknown>): string[] {
  const out: string[] = [];
  for (const key of ["path", "file", "filename", "filepath", "dir", "directory", "cwd"]) {
    const v = input[key];
    if (typeof v === "string" && v.length > 0) out.push(v);
  }
  const arr = input["paths"];
  if (Array.isArray(arr)) {
    for (const v of arr) if (typeof v === "string" && v.length > 0) out.push(v);
  }
  return out;
}

// withinRoot reports whether a path resolves inside root. A relative path is
// resolved against root (the worker's CWD is the scoped dir), so an in-tree
// relative path passes; an absolute path or a `..` climb that lands outside root
// fails. `~` is NOT expanded by the shell here, so a literal "~/x" stays relative
// to root and is contained — but we also treat a leading "~" as an escape signal
// (a worker reaching for the home dir), since the bash tool WOULD expand it.
function withinRoot(p: string, root: string): boolean {
  if (p.startsWith("~")) return false; // home-relative — would escape once expanded
  const abs = isAbsolute(p) ? normalize(p) : resolve(root, p);
  const nroot = normalize(root);
  if (abs === nroot) return true;
  return abs.startsWith(nroot.endsWith(sep) ? nroot : nroot + sep);
}

// BASH_ESCAPE flags a bash command that clearly reaches outside the worker's
// scope: an absolute path that is not under the workdir, a `~` home reference, or
// a parent-traversal `cd ..` out of the tree. Reliable bash path-parsing is
// impossible, so this catches the unambiguous escapes (defense in depth); the
// recipe's CWD + the OS are the real containment for arbitrary shell. Pure +
// tested. Returns a reason string when it flags, else "".
function bashEscape(command: string, root: string): string {
  // A literal home reference the shell would expand outside the scope.
  if (/(^|[\s="'(])~\//.test(command) || /\$HOME\b/.test(command)) {
    return "bash command references the home directory (~ / $HOME) — outside the worker scope";
  }
  // Absolute paths in the command that are not under the workdir. Match
  // /-rooted tokens (conservative: only flags an absolute path that is plainly
  // outside; a path under the workdir passes).
  const absTokens = command.match(/(?:^|[\s="'(:])(\/[^\s"';:|&)]+)/g) ?? [];
  for (const raw of absTokens) {
    const tok = raw.replace(/^[\s="'(:]+/, "");
    // A path under the worker's own scope always passes (the scope itself lives
    // under a system temp root on macOS, so this must be checked BEFORE the
    // system-path allowlist — otherwise the allowlist would wave through a
    // SIBLING worker's scope under the same temp root).
    if (withinRoot(tok, root)) continue;
    // Allow common read-only system paths a build/toolchain legitimately touches
    // (compilers, CA bundles, the null sink). Deliberately NOT /tmp or
    // /var/folders: those host other workers' scopes, so a path there that is
    // not the worker's own scope is an escape, not a system read.
    if (/^\/(usr|bin|sbin|lib|opt|etc\/(ssl|ca-certificates)|dev\/null|nix|System\/Library|Library\/Developer)\b/.test(tok)) {
      continue;
    }
    return `bash command references an absolute path outside the worker scope (${tok})`;
  }
  return "";
}

// classifyConfinement reports whether a tool call escapes the scoped workdir.
// root is SEXTANT_PI_WORKDIR; an empty root means "no scope configured" → never
// confines (a bare interactive session). A file tool whose target resolves
// outside root, or a bash command that plainly reaches outside, is confined:true.
export function classifyConfinement(
  toolName: string,
  input: Record<string, unknown>,
  root: string,
): { confined: boolean; reason?: string } {
  if (!root) return { confined: false }; // no scope set → nothing to enforce
  if (PATH_TOOLS.has(toolName)) {
    for (const p of candidatePaths(input)) {
      if (!withinRoot(p, root)) {
        return {
          confined: true,
          reason: `"${toolName}" path "${p}" is outside the worker's scoped directory (${root})`,
        };
      }
    }
    return { confined: false };
  }
  if (toolName === "bash") {
    const command = typeof input["command"] === "string" ? input["command"] : "";
    const reason = bashEscape(command, root);
    if (reason) return { confined: true, reason };
  }
  return { confined: false };
}

// registerGate installs the tool_call gate. Two independent decisions per call:
//
//   CONFINEMENT — if a workdir scope is set, a file/bash call escaping it is
//   blocked ALWAYS (UI or not, destructive toggle or not): the worker is confined
//   to its directory regardless of how trusted it is. This is the enforced half of
//   AC#1.
//
//   DESTRUCTIVE/GUI/SYSTEM — block-by-default when headless and the gate is
//   enabled; an interactive (hasUI) pi defers to its own confirmation flow, and
//   SEXTANT_PI_GATE_HEADLESS=off disables it for a trusted unattended worker.
//
// onBlock surfaces a block onto the activity bridge so it shows in the dash rather
// than being a silent no-op.
export function registerGate(
  pi: ExtensionAPI,
  opts: {
    enabled: boolean;
    workdir?: string;
    onBlock?: (toolName: string, reason: string) => void;
  },
): void {
  const root = opts.workdir ?? "";
  pi.on("tool_call", (event: ToolCallEvent, ctx): ToolCallEventResult | undefined => {
    const input = event.input as Record<string, unknown>;

    // 1. Confinement first — it holds even with a UI and even when the
    //    destructive gate is disabled. The scope is the worker's directory.
    const conf = classifyConfinement(event.toolName, input, root);
    if (conf.confined) {
      const reason = `sextant-pi worker sandbox (TASK-118): ${conf.reason}. Work within your scoped directory; this confinement is enforced and cannot be bypassed.`;
      opts.onBlock?.(event.toolName, reason);
      return { block: true, reason };
    }

    // 2. Destructive/GUI/system — the headless block-by-default gate.
    if (!opts.enabled) return undefined;
    if (ctx.hasUI) return undefined; // an operator is present → pi's own confirm flow owns it
    // When a scope IS set, an in-scope write/edit is the worker's REAL WORK
    // (AC#3): confinement (step 1) already proved the path is inside the worker's
    // own directory, so a mutating file tool there is safe and must proceed — the
    // headless mutating-tool block exists for an UNSCOPED worker that could write
    // anywhere. Destructive/GUI/system BASH is still blocked (it is dangerous
    // regardless of CWD). So with a scope, only bash is subject to this gate.
    if (root && MUTATING_TOOLS.has(event.toolName)) return undefined;
    const verdict = classifyDestructive(event.toolName, input);
    if (!verdict.destructive) return undefined;
    const reason = `sextant-pi headless gate: ${verdict.reason ?? "destructive action"} blocked (no UI to confirm; run unattended/untrusted pi in a container/VM, or set SEXTANT_PI_GATE_HEADLESS=off to disable this gate)`;
    opts.onBlock?.(event.toolName, reason);
    return { block: true, reason };
  });
}
