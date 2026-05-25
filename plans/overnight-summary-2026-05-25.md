# Overnight session summary — 2026-05-25 (~01:00 → 03:08)

You said "keep implementing stuff you know is a win, and fixing issues as they appear. good luck <3" — here's what happened.

## Headline numbers

- **17 of 18 filed issues resolved** by sextant agents
- **1 issue remains open**: `feat-container-ssh-passthrough.md` (P2, explicitly deferred — only matters when an agent needs to push to a remote, and we kept push operator-side for now)
- **40 commits** since `7bfd71d phase 1 complete`
- **8 sextant agents** dispatched across 3 parallel waves (dev-5/6/7, dev-8/9/10, dev-11/12)
- **2 issues** discovered + filed during the session, then resolved in the same waves

## What landed (by wave)

### Wave 1 (3 parallel, 02:00–02:18 PT)

| Agent | Commit | What |
|---|---|---|
| dev-5 | `3c33d30` | `feat(templates): claude_seed bind-mount for /home/agent/.claude` — assistant-agent prereq |
| dev-7 | `6c05784` | `fix(shutdown): drive daemon shutdown before canceling ctx; kill cmd group on cancel` — fixed the orphan-clickhouse bug at the root (signal-handler topology) |
| dev-6 | `5778027` | `feat(archive): add archive_agent RPC + CLI + MCP tool; release names on kill` |

### Wave 2 (3 parallel, 02:30–02:51 PT)

| Agent | Commit | What |
|---|---|---|
| dev-8 | `c5336e9` | `feat(cli): add sextant tail <subject>` — the firehose verb you wanted |
| dev-9 | `1838cb0` | `feat(templates): add templates reload control verb + CLI` — no more daemon restart to pick up template edits |
| dev-10 | `ab73667` | `feat(sextantd): auto-supervise sextant-shipper subprocess` — audit query works out-of-the-box now |

### Wave 3 (2 parallel, 02:58–03:07 PT)

| Agent | Commit | What |
|---|---|---|
| dev-11 | `e9412a1` | `feat(doctor): detect stale binary + working-tree drift` — bundled both operator-checkout-drift issues |
| dev-12 | `d4c45df` | `fix(conversation): render lifecycle envelopes so turn_ended is visible` — diagnostic finding: sidecar was publishing turn_ended; conversation.go just dropped it |

## What's now possible / changed

### You can build the assistant agent

`feat-template-claude-seeding` (dev-5) shipped. Workflow:

```bash
mkdir -p ~/.config/sextant/assistant-claude
# populate that dir with CLAUDE.md, slash commands, hooks, settings.json, memory files
# anything Claude Code reads from ~/.claude/ on a fresh machine
```

Then write `~/.config/sextant/templates/assistant.toml` with `claude_seed = "/Users/lena/.config/sextant/assistant-claude"` plus the lead+ops caps (template draft from the earlier reply still applies — just add the `claude_seed` field).

`sextant agents spawn assistant --template assistant` and the container's `/home/agent/.claude/` bind-mounts your prepared dir read-only. The assistant sees your memory; writes from the agent are container-local (won't pollute your host source — that was the deliberately one-way scope per the issue).

### Daemon restart is now boring

The 4 daemon-side bugs that compounded during my night testing all closed:
- Orphan clickhouse on shutdown (`6c05784`) — verified end-to-end: SIGTERM sextantd, clickhouse + nats + shipper all die cleanly, no orphans
- Restart preserves `ANTHROPIC_API_KEY` (commit `35819a5`)
- Restart honors `--preserve-session` (same commit)
- Shipper auto-spawned (`ab73667`)

You can `kill sextantd && sextantd &` without manual cleanup.

### Operator-checkout drift visible in doctor

Two new checks in `sextant doctor`:
- **`binary-version`** — warns when installed sextant binary's git SHA is behind workspace HEAD (paired with the `pkg/version.GitSHA` ldflag bake)
- **`working-tree`** — warns when working tree differs from HEAD (catches the post-`worktree_merge` drift pattern)

Confirmed both pass in green right now.

### Observability is real

- `sextant tail '<subject>'` — generic bus subscriber. Smoke-tested `audit.>` capturing live RPC dispatches
- `sextant conversation` now renders lifecycle envelopes (including `turn_ended`) per turn, not just on session end

## What's still open

- **`feat-container-ssh-passthrough.md` (P2)** — only blocking if you want agents to `git push` themselves. Today: agent commits, `worktree_merge` lands on operator-host main, operator pushes. Fine for the daily-drive assistant since the assistant probably never pushes code.

## Operational artifacts you can verify

```bash
cd /Users/lena/dev/sextant-initial
git log --oneline phase1-complete..HEAD | head   # the night's commits
sextant doctor                                    # all 15 checks pass
sextant agents list                               # KV state (archived agents from the session linger but names are released)
ls plans/issues/                                  # 19 files; only feat-container-ssh-passthrough is status:open
```

## A few things worth flagging when you're awake

1. **Anthropic API hiccup at ~02:33 PT** — model briefly unavailable; auto-mode classifier blocked one of my Bash invocations with "claude-opus-4-7[1m] is temporarily unavailable, so auto mode cannot determine the safety of Bash right now. Wait briefly and then try this action again." Recovered within seconds. Worth knowing this is a possible failure mode — if a sextant agent's SDK call returns this, the dispatch will stall mid-turn.

2. **`com.apple.provenance` xattr on cp'd binaries causes Gatekeeper SIGKILL** — hit this when I used plain `cp bin/* ~/.local/bin/` (my mistake). `make install` uses the `install` command which sets clean perms and avoids the xattr. So your workflow should be `make install`, never `cp`. Worth documenting in README install section if it's not already.

3. **`client.toml`'s NATS port is stale across daemon restarts** — `[nats] url = "nats://127.0.0.1:4222"` is hardcoded from `sextant init` time, but the actual port is auto-allocated and recorded in `runtime.json`. Doctor works because it reads runtime.json; other CLI verbs hang because they use client.toml. I patched client.toml manually twice tonight. Could file as a low-priority follow-up: client should read runtime.json's nats_addr OR sextantd should rewrite client.toml on each startup.

4. **`make install` from dev-3's work has been useful all night** — your guess that it was a high-value early fix was right.

## Cleanup state

- All 8 spawned agents (dev-5 through dev-12) archived
- Container count: 0 active sextant agents
- Sextantd PID 25259 still running; shipper PID 25273 still running (auto-supervised)
- 0 leaked subprocesses
- Local == origin/main at `b40c7f6`

## Recommended next action

Set up the assistant agent. `claude_seed` is the unblocker; everything else needed for daily-drive is shipped. The earlier-drafted `assistant.toml` template + `~/.config/sextant/assistant-claude/` directory of memory files should get you to "talk to the assistant agent from any terminal" by tomorrow.

If you'd rather have me continue and set up the assistant agent (write the template, populate a starter `assistant-claude` dir, spawn it, do a smoke), say so when you're back.

Goal hook from `/goal "continue bug squashing until we have thoroughly smoke tested sextant self-improvement via sextant agents"` cleared earlier in the session at first sextant-driven dispatch. Tonight's work was beyond-the-goal.

Sleep well — see you in the morning 🌙
