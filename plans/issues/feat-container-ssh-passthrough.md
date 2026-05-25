---
title: Add opt-in SSH key passthrough so agents can `git push`
status: fixed
priority: P2
created_at: 2026-05-24T23:18-07:00
labels: [feature, container, sidecar, git, auth]
discovered_in: pre-flight gap analysis
fixed_in: cafbfb28a09592a1cf611d10bfcbe45e63ff5582
---

## Summary

Per `specs/components/sidecar-image.md`:

> "The host's `~/.ssh` is not mounted by default; declare it per-agent if needed for git push."

But there's no mechanism in the agent template for an operator to declare it. So `git push` from inside an agent's container fails with `permission denied (publickey)`.

## Proposed fix

Add a new `mounts` class value `ssh` to the template schema. When present, the spawn handler bind-mounts `~/.ssh` (host) → `/home/agent/.ssh` read-only.

Template snippet:

```toml
mounts = ["worktree", "ssh"]
```

Default templates (`default.toml`, `lead.toml`) should **not** include `ssh` — it's opt-in. Only operators who trust an agent class enough to share their SSH keys should add it. A safer pattern long-term is per-agent ssh keys, but that's a v2 concern.

## Impact

Without this, agents can:
- Read the repo (the worktree mount has the local `.git`)
- Commit locally (once [[feat-container-git-config]] lands)
- BUT cannot push to GitHub — every push requires SSH auth

## Acceptance

`TestSSHMountWorks`: spawn an agent with `mounts = ["worktree", "ssh"]`, exec `ssh -T git@github.com` inside, assert the response includes `successfully authenticated`.

## Related

- `specs/components/sidecar-image.md`
- `specs/architecture.md` §11b (Templates schema needs the new mount class documented)
- [[feat-container-git-config]] (related; both needed for full commit + push flow)
