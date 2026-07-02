#!/usr/bin/env bash
# PreToolUse hook (Bash): refuse a commit/merge that would land on `main` in the
# checkout the command runs in. Sextant's discipline (CLAUDE.md / AGENTS.md): the
# primary checkout stays on main and clean; every tracked change lands via a
# sibling worktree + PR. This is the Claude-side complement to the git
# post-checkout hook (which only warns on branch switches).
#
# Reads the hook payload as JSON on stdin: .tool_input.command and .cwd.
# Exit 2 blocks the Bash call and feeds the message back to Claude; exit 0 allows.
#
# Bias: fail OPEN. We block only the unambiguous "bare commit/merge on main"
# case. A command that explicitly targets another tree — `git -C <path> ...` or a
# leading `cd <path> && ...` — is always allowed, since that is the worktree path.

input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty')
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty')

# Only guard commit/merge.
case "$cmd" in
  *"git commit"*|*"git merge"*) ;;
  *) exit 0 ;;
esac

# Explicit redirection to another tree → trust it (the worktree path).
trimmed=$(printf '%s' "$cmd" | sed -E 's/^[[:space:]]+//')
case "$trimmed" in
  cd\ *) exit 0 ;;
esac
printf '%s' "$cmd" | grep -qE '(^|[^[:alnum:]])git[[:space:]]+-C[[:space:]]' && exit 0

[ -n "$cwd" ] || cwd=$PWD
branch=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null) || exit 0

if [ "$branch" = "main" ]; then
  printf 'Refusing: this would commit to "main" in %s.\n' "$cwd" >&2
  printf 'Sextant keeps the primary checkout on main and clean — work on a sibling worktree and land via PR (see CLAUDE.md). Use "git -C <worktree>" or "cd <worktree> && ..." to target it.\n' >&2
  exit 2
fi
exit 0
