---
title: Sidecar bash classifier denies `rm -rf` anywhere — blocks legitimate `rm -rf /tmp/somedir`
status: fixed
priority: P3
created_at: 2026-05-25T14:53-07:00
fixed_in: 800eb77
labels: [bug, sidecar, classifier, permissions]
discovered_in: dev-3 first sextant-driven dispatch
---

## Summary

The bash classifier shipped at `cf8cbed` (Wave 1's prereq) blocks any command containing the substring `rm -rf`. The intent (per the issue's denylist) was to block `rm -rf /`, `rm -rf ~`, `rm -rf /workspace` — root/home/workspace nukes. The implementation is a `contains` match on the bare string `rm -rf`, which also matches benign cases like:

- `rm -rf /tmp/install-test` — exactly what dev-3 needed to do for its smoke test cleanup
- `rm -rf node_modules` (relative path)
- `rm -rf ./build`

dev-3 worked around by using `rm -r` (no `-f`), which the classifier doesn't catch. Smart agent behavior; bad classifier shape.

## Repro

1. Inside a sextant agent container (or just hit the classifier directly):
   ```typescript
   isDangerousBashCommand("rm -rf /tmp/foo")
   ```
2. Returns `{ behavior: "deny", label: "rm-rf-..." }` even though the path is a temp dir.

Visible in dev-3's conversation log captured during the first dispatch (`feat-make-install-target` smoke).

## Impact

- False positives on legitimate cleanup. Agents have to know to avoid `-rf` even on paths that are clearly safe.
- Operators or future agents trying to drop temp build artifacts hit the same friction.
- The implementer's note in `cf8cbed`: "rm -rf ./tmp and rm -rf build/ (relative paths) are intentionally allowed" — but the current contains-match doesn't actually allow them.

## Proposed fix

Replace the contains-match for `rm -rf` with anchored path checks:

```typescript
// Block only when the target path is root, home, or the worktree mount.
const RM_RF_DANGER = [
  /\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+\/(\s|$)/,        // rm -rf /
  /\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+\$HOME(\s|\/|$)/, // rm -rf $HOME or $HOME/
  /\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+~(\s|\/|$)/,      // rm -rf ~ or ~/
  /\brm\s+-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*\s+\/workspace(\s|\/|$)/, // rm -rf /workspace
  // sudo prefix denied separately, so no need to mirror for sudo rm
];
```

Plus the `-rfv`/`-rvf`/`-fvr` flag-permutation handling that the regex above accounts for (one `r` and one `f` somewhere in the flag block, in any order, possibly with other letters).

Anything not matching these specific patterns falls through to `allow`. Tests pin the safe cases (`rm -rf /tmp/x`, `rm -rf node_modules`, `rm -rf ./build`) and the dangerous cases (`rm -rf /`, `rm -rf ~/.ssh`, `rm -rf /workspace`).

## Acceptance

Extend `images/sidecar/entrypoint/test/classifier.test.ts`:

- `allows: rm -rf /tmp/install-test` — was previously denied
- `allows: rm -rf node_modules`
- `allows: rm -rf ./build`
- `allows: rm -rfv .next`
- still denies: `rm -rf /`, `rm -rf ~`, `rm -rf ~/`, `rm -rf $HOME/foo`, `rm -rf /workspace`, `rm -rfv /`, `rm -fvr /`

## Related

- `cf8cbed fix(sidecar): add canUseTool Bash classifier to unblock agents in acceptEdits mode`
- `images/sidecar/entrypoint/src/classifier.ts` — denylist patterns live here
- Sibling classifier issue: `[[bug-classifier-curl-multipipe-bypass]]` — different denylist gap, same file
