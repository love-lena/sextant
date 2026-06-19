# Architecture Decision Records

Decisions are recorded as ADRs: short, numbered, append-only. We supersede
rather than edit. `status: accepted` (with a name in `signed_off_by`) means a
human has signed off — see
[ADR-0002](0002-documentation-and-process-layout.md) for the process.

| #    | Title                                   | Status   |
|------|-----------------------------------------|----------|
| [0001](0001-vision.md) | Vision — what Sextant is  | accepted |
| [0002](0002-documentation-and-process-layout.md) | Documentation & process layout | accepted |
| [0003](0003-high-level-architecture.md) | High-level architecture (the component map) | accepted (sharpened by 0041) |
| [0004](0004-conventions-are-optional.md) | Conventions are optional, not core | accepted |
| [0005](0005-two-primitives.md) | The two primitives | accepted |
| [0006](0006-wire-atom.md) | The wire atom | accepted (refined by 0019) |
| [0007](0007-bus-is-nats-no-daemon.md) | The bus is NATS, and there is no daemon | accepted (refined by 0018) |
| [0008](0008-clients-are-processes.md) | Clients are processes | accepted (refined by 0020) |
| [0009](0009-spawn.md) | Spawn | accepted |
| [0010](0010-lifecycle-and-versioning.md) | Lifecycle & versioning | accepted (refined by 0019, 0020) |
| [0011](0011-workflows.md) | Workflows | accepted |
| [0012](0012-reserved-namespace-and-authn.md) | The reserved `sx` namespace, and authn | accepted (refined by 0015, 0020) |
| [0013](0013-multi-backend.md) | Multi-backend posture | accepted |
| [0014](0014-the-tui-is-a-client.md) | The TUI is a client | accepted (sharpened by 0023) |
| [0015](0015-operator-only-account.md) | Operator-only state lives in its own account | accepted |
| [0016](0016-artifacts-are-lexicon-records.md) | Artifacts are Lexicon records | accepted |
| [0017](0017-the-verb-surface-is-the-protocol.md) | The verb surface is the protocol | accepted |
| [0018](0018-the-bus-implements-the-protocol.md) | The bus implements the protocol over a pluggable stream backend | accepted |
| [0019](0019-implementing-the-bus.md) | Implementing the bus: call transport, frame stamping, and identity | accepted |
| [0020](0020-clients-are-bus-issued-identities.md) | Clients are bus-issued identities | accepted |
| [0021](0021-saved-client-contexts.md) | Saved client contexts | accepted |
| [0022](0022-modules-over-a-locked-core.md) | A locked core lets modules build in parallel | accepted |
| [0023](0023-the-dash-is-a-composable-pane-cockpit.md) | The dash is a composable pane cockpit | accepted (refined by 0024) |
| [0024](0024-the-dash-is-three-master-detail-browsers.md) | The dash is three master-detail browsers | proposed |
| [0025](0025-the-bus-keeps-its-address-across-restarts.md) | The bus keeps its address across restarts of the same store | proposed |
| [0026](0026-one-focused-pane-panes-hold-their-place.md) | One focused pane; panes hold their place | proposed |
| [0027](0027-subscriptions-survive-a-bus-restart.md) | Subscriptions survive a bus restart | proposed |
| [0028](0028-byo-harnesses-join-through-a-plugin-adapter.md) | BYO harnesses join through a plugin adapter | proposed |
| [0029](0029-a-harness-speaks-as-itself.md) | A harness speaks as itself, with a per-session identity | proposed (revises 0028's identity resolution) |
| [0030](0030-clients-act-on-a-principals-messages-as-operator-input.md) | A client acts on its principal's messages as operator-equivalent input | proposed |
| [0031](0031-claiming-the-principal-is-frictionless-re-pointing-is-deliberate.md) | Claiming the principal is frictionless; re-pointing it is deliberate | proposed (extends 0030) |
| [0032](0032-the-web-dash-is-a-face-on-a-local-api.md) | The web dash is a face on a local API | accepted (revised by 0041) |
| [0033](0033-a-dispatcher-mints-its-own-workers.md) | A dispatcher mints its own workers (mint-on-behalf) | proposed |
| [0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md) | The web cockpit rests on conventions, not new protocol | accepted (revised by 0041) |
| [0035](0035-the-goal-bus-primitive.md) | The goal bus primitive | accepted |
| [0036](0036-presence-and-liveness-derive-from-a-client-heartbeat.md) | Presence and liveness derive from a client heartbeat | accepted |
| [0037](0037-subscriptions-and-context-survive-a-session-resume.md) | Subscriptions and the active context survive a session resume | accepted |
| [0038](0038-a-remote-box-joins-through-a-leaf-node.md) | A remote box joins the bus through a leaf node | accepted |
| [0039](0039-the-assistant-is-a-convention-not-a-primitive.md) | The assistant is a convention, not a primitive | proposed |
| [0040](0040-agent-runtimes-run-as-os-managed-components.md) | Agent runtimes run as OS-managed components | accepted |
| [0041](0041-clients-are-co-equal-across-languages.md) | Clients are co-equal implementations of a language-neutral protocol | accepted |
| [0042](0042-the-curated-go-static-checks-gate.md) | A curated Go static-checks gate, paired with the house-style skill | proposed |

## Review batches
- **Batch 1 — substrate:** 0004–0007 — *accepted*
- **Batch 2 — clients & lifecycle:** 0008–0010 — *accepted*
- **Batch 3 — conventions & cross-cutting:** 0011–0013 — *accepted*
- **0014 — the TUI is a client** — *accepted* (grilled + signed off in-session, 2026-06-02)
- **0015 — operator-only state in its own account** — *accepted* (refines 0012; from the #71 review)
- **0016 — artifacts are Lexicon records** — *accepted* (the #70 JSON-vs-CBOR decision; signed off in-session 2026-06-03)
- **0023 — the dash is a composable pane cockpit** — *accepted* (sharpens 0014; grilled in-session prototype-grounded, signed off 2026-06-05)
- **0024 — the dash is three master-detail browsers** — *proposed* (refines 0023's composition after M4 dogfooding; grilled in-session 2026-06-08)
- **0025 — the bus keeps its address across restarts** — *proposed* (stable-address guarantee for enrolled contexts; TASK-35)
- **0026 — one focused pane; panes hold their place** — *proposed* (tmux-style focus replacing 0023's step-in/out; decided in-session 2026-06-09)
- **0029 — a harness speaks as itself** — *proposed* (revises 0028's identity resolution: the MCP adapter mints its own per-session identity, never the operator's active context; PR #107)
- **0030 — a client acts on its principal's messages as operator input** — *proposed* (the principal trust model; grilled in-session 2026-06-11)
- **0031 — claiming the principal is frictionless; re-pointing it is deliberate** — *proposed* (extends 0030: the first human seat claims the principal on `register --self`; re-pointing an established one is operator-only + `--force`; TASK-64)
- **0032 — the web dash is a face on a local API** — *proposed* (`sextant dash --serve` exposes the dash's one bus identity as a token-gated local HTTP API + SSE on 127.0.0.1, with a zero-design web debug surface; the browser never touches the bus; D1 of TASK-68)
- **0033 — a dispatcher mints its own workers** — *proposed* (mint-on-behalf: any registered client may call `clients.register` with its own authority EXCEPT a spawned worker — the fence is inverted from an allowlist and rests on a bus-stamped `SpawnedBy` marker, not the weakly-enforced kind, so a worker cannot recursively dispatch; the lone locked-core change of M5.2/TASK-25)
- **0034 — the web cockpit rests on conventions, not new protocol** — *proposed* (the designed web dash, D2 of TASK-71; review-state, per-artifact discussion topics, DM-as-2-party-topic, and subject discovery are conventions over the core protocol, served by `sextant dash --serve`)
- **0035 — the goal bus primitive** — *accepted* (TASK-84; a goal = a north-star + acceptance criteria with a **derived** status, the latest-value artifact `goal.<id>` + the `goal.update` stream on `msg.topic.goals`; evidence is declared artifact-side via a generic `relates`, met-criteria need ≥1 proof; signal-not-manage. Supersedes the parked coarse-state goal model. Shipped v0.5.0)
- **0036 — presence and liveness derive from a client heartbeat** — *accepted* (TASK-126; presence via a periodic client heartbeat (`agent.status` ping); hub derives online/idle/offline from cadence; unblocks accurate presence across leaf links. Shipped v0.5.0)
- **0037 — subscriptions and the active context survive a session resume** — *accepted* (TASK-124; the MCP adapter persists a session's manual subscriptions + each one's last-delivered seq + the `context_use` choice, keyed on the session id beside the attest cursor, and restores them on every connect — re-pin the context before auto-mint, re-subscribe + catch up by seq — so a resume/compaction/restart self-heals instead of silently dropping delivery. An adapter convention over `message_read` + `message_subscribe`; epoch unchanged; retires the interim keepalive. A seq-gap liveness watchdog composes with the 0036 heartbeat as a following slice. Shipped v0.5.0)
- **0038 — a remote box joins through a leaf node** — *accepted* (TASK-125; a remote box runs a local bus in leaf mode that federates the per-client wire-API subjects to the hub over one SEXTANT account, JetStream stays at the hub; the leaf installs the hub's PUBLIC account JWTs only — no seed → can't mint, enforces per-client perms locally → the hub's subject-derived author stamp stays trustworthy; presence via the ADR-0036 heartbeat, no new machinery; link rides a secure transport, native leaf TLS is a follow-up; additive + default-off. Shipped v0.5.0)
- **0039 — the assistant is a convention, not a primitive** — *proposed* (TASK-138/144/120; **violet**, the operator's assistant, unified as one client with two duties — *answer* read-only when messaged + *defend* the operator's attention by curating Home/inbox; named by the swappable latest-value `assistant` artifact `{client_id, name, accent}`; a convention over clients/artifacts/messages, zero new operations, signal-not-manage. Convention + dash entry points ship v0.5.0; violet **runtime ships v0.5.1**)
- **0040 — agent runtimes run as OS-managed components** — *accepted* (v0.5.3; the dispatch/violet/workflow runtimes ship in the Homebrew formula and are managed via `sextant components` over per-component launchd agents; the bus stays the single brew service — signal-not-manage)
- **0041 — clients are co-equal implementations of a language-neutral protocol** — *proposed* (the protocol — lexicon + conformance suite — is the product; the bus is implemented once in Go; the client surface (SDK, conventions, clients) is co-equal across languages, conventions are lexicon-defined libraries verified by conformance, and the tree is organised by what things are rather than Go visibility buckets (no top-level `pkg/`); forced now with a TS SDK + pi harness extension as the first non-Go client; sharpens 0022)
- **0042 — a curated Go static-checks gate, paired with the house-style skill** — *proposed* (TASK-181, realises TASK-17; the gate (`make lint` + CI) is curated — high-value, low-friction, **zero `//nolint` debt** — enabling govet/errcheck/errorlint/ineffassign/staticcheck + the `importcheck` bright lines, with `_test.go` errcheck-relaxed; a check that can't run clean against a legitimate idiom becomes a **skill convention** instead (the 5 calibration calls: containedctx, mutable globals, deep-modules/no-new-pkg, error-wrapping policy, test exclusions); whole post-172 tree passes clean, fixes not suppressions; signed off at the m6→main merge)
