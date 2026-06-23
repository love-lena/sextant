---
status: proposed
date: 2026-06-19
---

# The pi harness is a first-class bus client

A pi coding-agent session can be a full member of a sextant bus: its own scoped
identity, addressable by anyone, woken by a message, observable in the dash, able
to publish, read, share artifacts, and move a goal. This ADR records the decision
(TASK-176 spike, TASK-177) to make pi a first-class bus client through a single
in-process extension — `@sextant/pi-bus` (`clients/ts/pi`) — and the trust and
security model that membership rests on.

It extends two earlier decisions to pi. [ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md)
established that a bring-your-own harness joins through an adapter rather than the
core learning about it; the pi extension is that adapter for pi, written against
the language-neutral protocol via the co-equal TypeScript SDK
([ADR-0041](0041-clients-are-co-equal-across-languages.md)). [ADR-0039](0039-the-assistant-is-a-convention-not-a-primitive.md)
made an agent's role a convention over the existing primitives; the pi client is
a convention in the same spirit — it adds no bus operation, and everything it
does (the wake, the activity stream, `/set-goal`) is ordinary publish / read /
subscribe / artifact traffic that any client could issue.

## A harness extension, not a new protocol

The extension opens an SDK `Client` on the pi agent's **own** scoped credential
at `session_start` and drains + closes it at `session_shutdown`. An inbound bus
frame on the agent's inbox or a watched topic is injected into the agent loop as
an ordinary message that triggers a turn — the wake. The agent's turns, thinking,
and tool calls are bridged onto a per-agent `pi.activity` topic that the dash's
generic conversation viewer renders live, so a headless pi worker shows up like
any crew member (the TASK-150/151 observe-from-sextant thread). Goals move through
the goals convention ([ADR-0035](0035-the-goal-bus-primitive.md)) — the same
`goal.<id>` artifact and `goal.update` stream the dash reads — never a hand-rolled
write. The bus learns nothing new; pi gains a membership.

The TASK-176 spike validated this against a real bus and a real pi process and
surfaced five design facts the production extension carries:

1. **`session_start` is idempotent.** pi fires `session_start` (reason `"new"`)
   twice for a single `new_session` in RPC mode. The open path is
   close-before-open and self-serialising, so no client leaks and the
   second-fire window cannot miss a frame.
2. **Back-pressure is bounded, drop-oldest, with a reserved direct slot.** A wake
   is "come look at the bus", not at-least-once delivery — the durable record
   lives on the bus, so dropping the oldest queued *topic* wake under a flood
   loses no content (the agent can read the topic to recover). Direct address (an
   inbox DM) holds a reserved slot and is delivered first, so a topic flood cannot
   starve it; a same-author/same-topic burst coalesces into one wake. The queue
   drains one per `turn_end`, so turns never stack unbounded.
3. **The observability bridge is first-class**, shaped as the `pi.activity`
   lexicon (`protocol/lexicons/pi.activity.json`): a small vocabulary — turn
   markers, the tool name/args/result, the thinking and reply text — so the dash
   renders the worker without attaching to its terminal.
4. **Security is layered and explicit** (below).
5. **The pi version is pinned** (`0.79.8`); the driven harness re-validates these
   facts on a bump.

## Trust and security: bus content is untrusted input

pi runs with the user's permissions and is **not a sandbox**; project-trust guards
which inputs load, not what tools may do, and prompt injection from untrusted
content is an expected local-agent risk. A bus frame enters pi as ordinary input —
a custom message that triggers a turn — so it is exactly as powerful, and exactly
as untrusted, as a typed prompt. A bus message that says "delete everything" is a
prompt-injection attempt from whatever identity actually sent it. The defenses are
layered, and none alone is sufficient:

- **Own scoped credentials, least privilege.** The agent acts on its own
  bus-minted identity (ADR-0020), never the operator's ambient context. The
  bus-stamped frame author is unforgeable, so a pi worker is a co-equal crew
  member, not an operator impersonator; a compromised worker can only reach what
  its credential was granted.
- **A headless, block-by-default tool gate.** When there is no UI to confirm (the
  unattended case, `!ctx.hasUI`), destructive tool calls — irreversible filesystem
  loss, privilege escalation, over-broad permission changes, remote-exec pipes,
  force-push, fork bombs, and the filesystem-mutating built-ins — are blocked by
  default and surfaced on the activity topic. With a UI present the gate defers to
  pi's own confirmation flow so an interactive session is not hobbled. The default
  is overridable (`SEXTANT_PI_GATE_HEADLESS=off`) for a trusted run inside a real
  isolation boundary.
- **The OS boundary is the real isolation.** For untrusted or unattended work, run
  pi in a container or VM, per pi's own guidance. The in-process gate raises the
  floor; it does not replace the boundary.
- **The wake is trust-tiered by author.** The extension stamps each injected wake
  with the author's trust tier — principal (operator-equivalent, ADR-0030),
  verified peer, or unknown — derived from the unforgeable author ULID, mirroring
  the `sextant:startup` trust-stamped-message pattern. The model and the gate weigh
  an instruction by its source; a directive from an unknown client is a stranger's
  request, not an order.

Trust is the author ULID, never the content — a message that *claims* to be the
operator is untrusted content from whatever ULID sent it.

## Headless under a dispatcher, with a managed handoff

The primary way a pi worker runs is headless under the reference dispatcher
([ADR-0033](0033-a-dispatcher-mints-its-own-workers.md)): a `pi` recipe
(`clients/go/apps/dispatch/recipes/pi.sh`, a sibling of the `claude` recipe)
launches `pi --mode rpc` with this extension, on a child identity the dispatcher
**mints with its own authority** — never the operator's credential. The worker
boots idle and is woken over the bus; it is a crew member that happens to be a pi
session (TASK-178).

A headless worker also needs a **managed close-and-resume handoff** — to move it
to another box, resume it by hand, or re-spawn it fresh — without two processes
fighting one pi session. The discipline is **single-owner-at-a-time**, coordinated
over the bus with a `pi.handoff` convention (`protocol/lexicons/pi.handoff.json`),
the same shape as `workflow.control`: it **asks, it does not compel**.

- An owner sends `pi.handoff{verb:"drain"}` to the worker's inbox. Because it is a
  recognised control record on the inbox, the extension routes it to the wind-down
  and does **not** inject it as a wake — control is not a task the model sees. A
  drain is a privileged action (it stops the worker and releases the session), so it
  is gated by the same trust rule as the tool gate: only the **principal or a
  verified peer** may drain; an unknown client's drain is refused and falls through
  to the ordinary, tier-stamped wake path, where it is impotent — trust is the
  author, never the content.
- The worker winds down **cooperatively**: it stops taking new wakes, lets the
  current turn finish, announces `pi.handoff{verb:"relinquished", session:<id>}`
  on a shared topic (naming the persisted session), drains + closes its bus client
  so presence drops to offline — the *visible* release — then calls pi's
  `ctx.shutdown()` to exit. pi has already persisted the session as JSONL.
- The operator resumes the JSONL by hand; the dispatcher re-spawns the recipe under
  the **same** pi session id (a stable id derived from the child's bus id, so
  `pi --session-id` resumes rather than restarts). On resume the extension
  announces `pi.handoff{verb:"acquired"}`.

Single-owner falls out of the sequence: the relinquish completes — the worker
process gone, presence offline — *before* any re-spawn acquires the session, so the
two never overlap. As with a wedged workflow coordinator, force-stopping a wedged
worker is the OS's job via the dispatcher that launched it; the in-process
wind-down is bounded and then yields. A resume is detected intrinsically (the
session already has entries at `session_start`), not from pi's `session_start`
reason, which reports `"startup"` for a `--session-id` resume.

## Consequences

A pi session becomes a participant the operator can DM and watch, with the same
identity, observability, and goal mechanics as any other client — the co-equality
ADR-0041 calls for, now realised across a second harness as well as a second
language. The cost is a standing security responsibility: bus-delivered content is
an untrusted prompt-injection surface, the in-process gate is a floor not a
sandbox, and an unattended pi worker on an untrusted bus belongs behind an OS
boundary. The extension adds no bus operation and no locked-core change; it is a
client-side convention over the existing primitives, signed off at the m6→main
merge.
