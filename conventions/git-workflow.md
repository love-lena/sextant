# Git workflow — sextant

Sextant's working model is **many parallel agents, each on their own git worktree**. This document describes the conventions for branching, committing, merging.

## Worktrees

The repo at `/Users/lena/dev/sextant-initial/` is the operator's main worktree (post-cutover: `/Users/lena/dev/sextant/`). Agent worktrees live at `/Users/lena/dev/sextant-worktrees/<branch-name>/`.

Each worktree:
- Has its own working tree (independent file state)
- Shares the `.git` database with the main repo
- Is mounted into one agent's container as `/workspace`
- Has its own branch checked out

Worktrees are created/destroyed via the `worktree_create` / `worktree_destroy` MCP tools. The worktree registry lives in NATS KV at `worktrees.<name>`.

## Branch naming

`<kind>-<short-description>-<seq>`

Where:
- `kind` ∈ `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `spec`
- `short-description` is 2-5 words, kebab-case
- `seq` is a 3-digit counter for collision avoidance (agents may try the same description)

Examples:
- `feat-bus-routing-001`
- `fix-clickhouse-migration-003`
- `spec-nats-component-001`

Branches are created from `main`. Long-lived feature branches are discouraged; prefer small, mergeable units.

## Commits

- Atomic: one logical change per commit.
- Imperative subject: "add X" not "added X" or "adds X".
- Subject line ≤ 72 chars.
- Body explains *why*, not *what* — the diff shows what.
- Co-authored-by trailers for AI-generated commits: `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`.
- Reference issues/specs with `Spec: specs/components/nats.md` or `Plan: plans/bootstrap.md#M2` lines in the body when relevant.

## Merging

Merges into `main` are serialized via the `locks.merge` NATS KV key (bucket `locks`, key `merge`, TTL 5 min). Only one merge at a time prevents conflicts from cascading. The lock value is the holder's UUID/host + a unix-nano timestamp so a crashed holder is identifiable in `worktree_list` output and `audit.worktree_merge` envelopes.

Merge flow (via the `worktree_merge` MCP tool — see `specs/architecture.md` §11 "Merge strategy" for the rationale):
1. Acquire `locks.merge` (or wait).
2. Create a transient merge worktree at `<WorktreesRoot>/.merge-<target>-<short-rand>/` on the target branch (replacing any stale `.merge-*` left from a crashed prior merge).
3. Run `git merge --no-ff <branch>` in the transient worktree.
4. On conflict: `git merge --abort`, tear down the transient worktree, release the lock, return a conflict report → becomes a user-input request (§4a). The source worktree is unchanged.
5. On clean merge: tear down the transient worktree (`git worktree remove`), update the source worktree's KV entry to `status=merged`, release the lock.
6. Source worktree is now safe to destroy or kept for follow-up work.

The operator's main checkout (typically `/Users/lena/dev/sextant-initial/`) is never touched during a merge — the dedicated transient worktree owns the merge commit, and the target ref advances in the shared `.git` database.

Reviewers (lead agent, or a dedicated review agent) inspect the diff via `worktree_diff` before approving a merge. Merge without review is technically allowed but capability-gated.

## Conventions specific to AI agents

- **No `git push --force` ever**, on any branch, in any worktree. This rule has no exceptions. Force push to a shared remote is a destructive action that requires explicit operator authorization.
- **No `git rebase -i` or rewriting committed history.** Mistakes get fixed with new commits.
- **Don't `git add -A`** in a worktree. Add specific files; don't sweep in `.env`, secrets, generated files.
- **Don't amend.** New commit instead, always.
- **Don't switch branches inside a worktree.** Each worktree is pinned to one branch. To work on a different branch, get a different worktree.

## Disk hygiene

- Worktrees idle > 14 days → archived (moved to `~/.local/share/sextant/worktree-archive/`)
- Worktrees idle > 30 days → deleted
- Build caches (`~/go/pkg/mod`, `~/.cache/go-build`) shared across worktrees via mounted volumes — avoids per-worktree rebuilds of deps

The pruner that enforces the above is **opt-in and safe-by-default**:

- `sextant worktree prune` defaults to dry-run. Pass `--apply` to act.
- The daemon-side periodic ticker is off by default. Set `[worktree] auto_prune = true` in `sextantd.toml` to enable hands-off cleanup. Verify with `sextant worktree prune` (dry-run) first.
- Directories on disk without a corresponding entry in the worktrees KV registry ("orphans") are NEVER deleted automatically. The operator must pass `--orphan-delete` to opt into removing them. This protects operator-curated dirs that happen to live in `worktrees_root` but were created outside sextant.

## Open

- Branch naming for spec-only changes vs code: same scheme, just `spec-` prefix?
- How aggressively do we cherry-pick across branches? Probably avoid — encourage merging into main first.
