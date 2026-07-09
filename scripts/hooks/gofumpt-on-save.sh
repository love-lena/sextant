#!/usr/bin/env bash
# PostToolUse hook (Edit|Write|MultiEdit): gofumpt -w any Go file Claude just
# edited, so a worktree never lands in CI with a formatting failure. CI enforces
# gofumpt (stricter than gofmt — a gofmt-clean file still fails); this mirrors
# `make fmt`. Best-effort: never blocks the edit, only formats.
#
# Reads the hook payload as JSON on stdin; the edited path is .tool_input.file_path.

file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null) || exit 0

case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac

command -v gofumpt >/dev/null 2>&1 || exit 0   # no-op if gofumpt isn't installed
if [ -f "$file" ]; then
  gofumpt -w "$file" || true                   # tolerate a mid-edit parse error
fi
exit 0
