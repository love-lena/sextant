# Known gaps and drift

Places where the code and the specs disagree, where a feature is documented but not built, or where behaviour is surprising in a way worth knowing. Snapshot commit `73462f3`.

## Not implemented (spec mentions them; code doesn't)

### RPC verbs

- `read_file_stream` — spec'd at `specs/protocols/rpc-catalog.md` and listed at `specs/protocols/bus-subjects.md:74`. Not in `pkg/rpc/handlers/`. Non-streaming `read_file` works.
- `trigger_thought_dump` — spec'd. Not implemented.
- `enable_verbose_logging` — spec'd. Not implemented.
- `get_session_summary` — spec'd at `specs/protocols/rpc-catalog.md:26`. Not implemented. The data is reachable via `query_history`.
- `self_update`, `self_rollback`, `query_deploy_history` — M16. Not implemented.
- `provision_test_environment`, `teardown_test_environment`, `list_test_environments`, `connect_to_test_environment` — M17. Not implemented.

### MCP tools

The MCP catalog (`pkg/mcpserver/tools.go:58`) ships 17 tools. The architecture spec §9c describes additional categories that aren't yet wired:

- `subscribe_to_subject`, `wait_for_agent_to_finish` — communication-category tools described in §9c. Not in `AllTools()`.
- `query_history` (as MCP, not RPC) — spec lists it under introspection; sidecars currently reach it through the operator-style RPC path.
- `restart_agent` (as MCP) — present as an RPC verb, not surfaced as an MCP tool today.

### Daemon signals

- `SIGHUP` — `cmd/sextantd/main.go:97` logs "not yet implemented (M5)". Reserved for re-reading config.
- `SIGUSR2` — `cmd/sextantd/main.go:98-99` logs "stub". Reserved for the M16 self-update execv handoff.

### TUIs

- The conversation viewer, pending queue TUI, audit browser, and worktree browser described in conventions/spec text. None exist at this snapshot. Only `sextant-tui-agents` (M13) ships.

### Test environments (M17)

- `test_envs` KV bucket is created at NATS bootstrap (`pkg/natsboot/layout.go`) but no code reads or writes it.

### Multi-host federation (architecture §7)

- The architecture describes worker certs, the operator host concept, and a `~/.config/sextant/cluster.toml`. Multi-host is not exercised at this snapshot. The default deployment is single-host.

### User-input propagation (architecture §4a)

- `user_input.requests.<from_uuid>` / `user_input.responses.<request_id>` are wire-level present (KV bucket exists, payload types defined). The "layered review / batching" UX is not built.

## Surprising defaults

- `daemon.shutdown_timeout` defaults to `30s` (`pkg/sextantd/config.go:166`).
- The MCP HTTP listener defaults to `127.0.0.1:5172` (`pkg/sextantd/config.go:191-192`).
- Sidecars connect to NATS as the operator user (via env-var creds), not under their own NATS user — the M11 transitional state. The per-agent JWT is only used by the MCP server, not NATS.
- `agents restart` **defaults to dropping the session id**. Pass `--preserve-session` to forward `SEXTANT_SESSION_ID` to the new container (`pkg/rpc/handlers/restart.go:167-170`). Without the flag, the SDK starts a fresh session and the next turn writes a new id back.

## Drift to be careful about

### `bus-subjects.md` lists subjects that don't exist on the bus

`specs/protocols/bus-subjects.md` enumerates `sextant.rpc.read_file_stream`, `sextant.rpc.trigger_thought_dump`, and others (see RPC verbs above). The dispatcher doesn't subscribe to or handle them; they're spec aspirations.

### Operator authority is via Unix file perms, not a JWT

The operator's NATS user is `operator.creds`. Operator MCP access is via the stdio Unix socket. There is no operator JWT today, and `audit.access` envelopes record `actor=operator` as a literal string rather than a UUID. The spec at §10b makes this explicit; readers coming from the architecture's mentions of "operator JWT (v2)" should not expect it here.

### `worktree.repo_root = ""` silently disables worktree wiring

If you forget to set this in `sextantd.toml`, the daemon comes up healthy but `worktree_*` RPCs and MCP tools return errors. Config validation does **not** require it; the empty value is documented as the M14 transitional state. The `sextant doctor` output may not flag this.

### Generated TS types live in a single bundled file

`clients/typescript/src/types.generated.ts` is one file containing every schema's `$defs` merged. Editing it by hand is a non-starter — re-run `make ts-codegen` instead.

## Old README says "no code yet"

`README.md` was written before phase 1 ran. The current `README.md:30` still says "Right now: **specifications, plans, and conventions only**. No code yet." That sentence is wrong as of this snapshot — there are 32k+ lines of Go plus the TypeScript client and sidecar. Trust this book or the source, not the README's preamble.

## Where to file new drift findings

File them in `backlog/` with the Backlog.md CLI: `backlog task create "…" -l bug,<area>` (slug naming `bug-*` / `feat-*` is preserved as a `slug:` label). The `backlog` skill (`.claude/skills/backlog/SKILL.md`) covers the priority ladder and what-to-file. For the currently-open set, run `backlog task list --plain` — listings rot quickly here.
