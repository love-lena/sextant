# NATS module notes

Internal development notes for the NATS backend module. This page describes how the
NATS module implements the backend interface (`semantic-contract.md`) so the bus
can serve its operations. It is not user-facing: clients never speak NATS — they
call the bus over the Wire API, and the bus runs on this module. NATS is named
freely here because this is the NATS module's own document.

NATS provides three facilities the module uses:

- JetStream streams for the durable message log;
- JetStream KV buckets for artifacts, metadata, and reference conventions;
- core pub/sub for cooperative drain broadcast.

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
| `sx.control.drain` | Cooperative drain broadcast. |
| `sx.workflow.*` | Workflow convention subjects. |

Messages use the `msg.>` subject space. The reference naming conventions are
`msg.topic.<name>` and `msg.agent.<id>`.

## Auth and identity

The module uses decentralized NATS JWT auth: one account and one user JWT per
client. The client id is the user-name claim in the credentials JWT. The bus takes
that authenticated name as the frame's `author`, the registry key, and the identity
behind SDK-facing APIs. The client does not assert it.

## Connect handshake

When a client connects, the bus drives this module as follows:

1. Resolve the NATS URL.
2. Read the client id from the credentials JWT user-name claim.
3. Connect with the credentials and client name.
4. Read `sx_meta/epoch` and refuse to proceed unless it exactly matches the
   protocol epoch.
5. Write the client's registry entry to `sx_clients/<id>`.
6. Subscribe to `sx.control.drain` and flush before reporting the connection ready.

On clean close, the module deletes the `sx_clients/<id>` entry best-effort, then
closes the connection.

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
| `clients.list` | List keys in `sx_clients`; read each value; decode the `client` lexicon; reject records whose body id differs from the key. |

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
