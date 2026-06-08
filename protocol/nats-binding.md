# NATS module notes

Internal development notes for the NATS backend module. This page describes how the
NATS module implements the backend interface (`semantic-contract.md`) so the bus
can serve its operations. It is not user-facing: clients never speak NATS — they
call the bus over the Wire API, and the bus runs on this module. NATS is named
freely here because this is the NATS module's own document.

The backend-neutral **call protocol** an SDK implements — the connect handshake's
shape, the delivery shapes, and frame stamping — is in `wire-api.md`. This page
covers only how the NATS module *realizes* it.

NATS provides three facilities the module uses:

- JetStream streams for the durable message log;
- JetStream KV buckets for artifacts, metadata, and reference conventions;
- core pub/sub for each client's private push delivery (`sx.deliver.<id>.*`),
  which carries subscribe/watch results and the cooperative-drain signal.

## Topology

The module provisions this topology before the bus serves clients.

| Name | Kind | Purpose |
|---|---|---|
| `MESSAGES` | JetStream stream | Captures `msg.>` subjects. |
| `ARTIFACTS` | KV bucket | Stores artifact records. |
| `sx_meta` | KV bucket | Stores public protocol metadata such as `epoch`. |
| `sx_clients` | KV bucket | Stores the clients registry convention. |
| `sx_workflows` | KV bucket | Reserved for workflow conventions. |

Reserved subjects:

| Subject | Purpose |
|---|---|
| `sx.control.*` | Operator-only control subjects. Clients may not publish here. |
| `sx.deliver.<id>.*` | A client's private push-delivery space: subscribe/watch results and the cooperative-drain signal (`sx.deliver.<id>.drain`). |
| `sx.workflow.*` | Workflow convention subjects. |

Messages use the `msg.>` subject space. The reference naming conventions are
`msg.topic.<name>` and `msg.client.<id>`.

## Auth and identity

The module uses decentralized NATS JWT auth: one account and one user JWT per
client. The client id is the user-name claim in the credentials JWT. The bus takes
that authenticated name as the frame's `author`, the registry key, and the identity
behind SDK-facing APIs. The client does not assert it.

The bus is the **sole minter** of identities (ADR-0020): the account signing key
lives in the bus and nothing else holds it. A credential is obtained by calling
`clients.register`, which mints a ULID and a per-client JWT and persists the
identity record — there is no offline minting. Two reserved identities authorize
that call: **`operator`** (held-identity mode — `sextant up` provisions its
credential in the store) and **`enroll`** (the bootstrap/enrollment connection
tier — its credential, also provisioned at `up`, may publish *only*
`sx.api.enroll.clients.register`, so an identity-less local process can reach the
issuance path and nothing else). Both are minted credentials, not signing keys;
locality is the trust (a process that can read the store is on the operator's box).

## Connect handshake — NATS specifics

The handshake's shape is backend-neutral and defined in `wire-api.md`; this is how
the NATS module realizes each step. A client connects with a credential it was
already issued (it does not register on connect — register *issues*, it is not a
connect step).

1. Resolve the NATS URL.
2. Read the client id from the credentials JWT user-name claim — this is the
   authenticated identity the Wire API handshake relies on.
3. Connect with the credentials and client name, using the per-client inbox prefix
   `_INBOX.<id>` that matches the credential's allow-list.
4. `clients.hello` confirms `sx_clients/<id>` exists (issued, not retired) and
   returns `{bus_epoch, server_time}` for the epoch hard-gate and the soft
   clock-skew check.
5. Subscribe to `sx.deliver.<id>.drain` and flush before reporting the connection
   ready.

Presence is read from the embedded server's connection table (`Connz`, with
`Username` set so the authenticated subject is populated): a client is `online` iff
an authenticated connection for its subject exists. On clean close the module just
closes the connection — the identity persists in the directory as `offline`;
removal is `clients.retire`, a deliberate decommission.

## Operation binding

| Operation | NATS realization |
|---|---|
| `message.publish` | Stamp the frame and publish it to a subject under `msg.>` on the `MESSAGES` stream. Return the stream sequence. |
| `message.read` | Create an ephemeral consumer on `MESSAGES` filtered to the subject. Cursor `0` means `DeliverAll`; any other cursor is the next stream sequence to read. Fetch up to `limit`; return `next_cursor = last_sequence + 1`. |
| `message.subscribe` | Create an ordered consumer on `MESSAGES` filtered to the subject. Use deliver-new by default, or deliver-all when requested. Decode and validate each frame before delivery; skip invalid messages. |
| `artifact.create` | `ARTIFACTS.Create(name, record)`. The value is the bare lexicon record. Fails if the key exists. |
| `artifact.update` | `ARTIFACTS.Update(name, record, expected_rev)`. Fails if the current revision differs. |
| `artifact.get` | `ARTIFACTS.Get(name)`. Return the bare record, revision, and timestamps. |
| `artifact.delete` | `ARTIFACTS.Delete(name)`. Delete is unconditional in the reference surface. |
| `artifact.watch` | `ARTIFACTS.Watch(name)`. Deliver the current value first, then later writes and deletes. |
| `clients.list` | List keys in `sx_clients`; read each value; decode the `client` lexicon; source the id from the key. Join with presence: `online` iff the record's authenticated subject has a live connection (`Connz`), else `offline`. Offline identities are listed too. |
| `clients.register` | Authorize the caller (`operator` or `enroll`); mint a new ULID + per-client JWT from the account key; persist the record to `sx_clients/<id>`; return `{id, creds}`. |
| `clients.retire` | Authorize the caller (`operator`); delete `sx_clients/<id>`; drop any live connection authenticated as that subject. |
| `clients.hello` | Connect handshake (not in `methods.json`): confirm `sx_clients/<id>` exists (else reject — unissued/retired); return `{bus_epoch, server_time}`. |

## Framed versus bare

Messages are framed. Artifacts and registry entries are stored bare; the bus
assembles the artifact frame (revision, timestamps) from the record plus KV
metadata on read.

| Data | Stored value |
|---|---|
| Message on `msg.>` | `frame` lexicon wrapping a record. |
| Artifact in `ARTIFACTS` | Lexicon record directly. |
| Client entry in `sx_clients` | `client` record directly. |

Data published to `msg.>` outside the bus may be accepted by NATS, but compliant
receivers skip it during validation. Artifact and registry operations fail or
surface as corrupt according to their read/write rules.
