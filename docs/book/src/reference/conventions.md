# Conventions

A short summary of the conventions every contributor (human or agent) follows. The authoritative source is the `conventions/` directory in the repo; this page is a pointer + the things every reader benefits from knowing.

## Go style — `conventions/STYLE.md`

Baseline: **Uber Go style guide**. Sextant additions on top.

Hard rules enforced by lint or review:

- `gofumpt` over `gofmt`. `goimports` with local prefix `github.com/love-lena/sextant`.
- Acronyms keep consistent case: `URLParser`, `httpClient`.
- No `Get` prefix on getters.
- Always wrap errors with `%w`: `fmt.Errorf("loading config: %w", err)`.
- Use `errors.Is` / `errors.As`. No string comparison on errors.
- Error variables prefixed `Err`: `ErrAgentNotFound`.
- Never return `(partial, err)`. Return `(nil, err)` or `(zero, err)`.
- Prefer value types over pointer types. Design zero value to be useful.
- Constructor pattern is mandatory when the zero value is invalid.
- `nilaway` runs in fail-build mode. Suppress only with a `//nolint:nilaway // <reason>`.
- `context.Context` is the first argument to anything that can block.
- Channels owned by their writer.
- Small interfaces (1–3 methods) defined on the consumer side.
- No `init()` functions.
- `panic` only in `main` or for invariant violations.
- No `any` outside serialization boundaries.

Lint surface (`make lint`):

- `golangci-lint` v2 with `govet`, `staticcheck`, `errcheck`, `gosec`, `revive`, `gocritic`, `unused`, `ineffassign`, `errorlint`, `bodyclose`, `contextcheck`, `copyloopvar`.
- `nilaway` standalone (golangci-lint v2 doesn't bundle it).
- `tsc --noEmit` for `clients/typescript/` and `images/sidecar/entrypoint/`.

## Git workflow — `conventions/git-workflow.md`

- Many parallel agents, each on their own git worktree.
- Branch naming: `<kind>-<short-description>-<seq>` (kind ∈ `feat, fix, refactor, docs, test, chore, spec`).
- Atomic commits. Imperative subject, ≤ 72 chars.
- Co-authored-by trailer for AI-generated commits:
  ```
  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  ```
- Reference issues/specs in commit bodies with `Spec: …` / `Plan: …` lines.
- Merges into `main` go through `worktree_merge`, serialized by the `locks.merge` NATS KV key.

Hard rules:

- **No `git push --force` ever.** Force-push to a shared remote is destructive and requires explicit operator authorization.
- **No `git rebase -i` or rewriting committed history.** Fix mistakes with new commits.
- **Don't `git add -A`.** Add specific files.
- **Don't amend.** New commit instead.
- **Don't switch branches inside a worktree.** Each worktree is pinned to one branch.

## TUI / CLI conventions — `conventions/tui-conventions.md`

- Go TUIs use Bubble Tea + Lipgloss. TS UIs use Ink.
- Client library is mandatory: `pkg/client` for Go, `@sextant/client` for TS. Don't talk to NATS directly from a TUI.

Standard keymap (TUIs must not override these):

| Key                | Action                                |
|--------------------|---------------------------------------|
| `q` / `Ctrl+C`     | Quit                                  |
| `?`                | Help overlay                          |
| `Esc`              | Cancel / close help                   |
| `j` / `↓`          | Next item / scroll down               |
| `k` / `↑`          | Previous item / scroll up             |
| `g`                | Top                                   |
| `G`                | Bottom                                |
| `/`                | Search (where applicable)             |
| `n` / `N`          | Next / previous match                 |
| `Enter`            | Select / open                         |
| `Tab` / `Shift+Tab`| Next / previous focus area            |
| `r`                | Refresh / reload                      |

Status-bar layout:

```
<context info>                            <pending count>  <key hints>
```

CLI conventions:

- Every command supports `--json`.
- Default output is human-readable; paginate through `less -FX` when interactive.
- Exit codes: 0 success, 1 user error, 2 system error.
- Long-running commands print status to stderr, results to stdout.
- Canonical shape: `sextant <noun> <verb>` (e.g. `sextant agents create`).

`ui.state.*` keys: per-operator, sanitized to `[a-zA-Z0-9_-]+`. See [Bus subjects](../protocols/bus-subjects.md) §`ui.state.*` for the on-the-wire key shape.

## Where conventions live

- `conventions/STYLE.md` — full Go style.
- `conventions/git-workflow.md` — branching, commits, merge lock.
- `conventions/tui-conventions.md` — keymap, status bar, theme tokens, `ui.state.*`.

When this book and `conventions/` disagree, `conventions/` wins.
