# The verb surface

The domain verbs, defined once as the protocol and exposed through every face.
Each verb carries a **delivery mode** — the property that decides which faces
expose it and that the CLI↔MCP conformance test enforces.

| Delivery | Verbs | CLI | MCP tool | Channel (push) | SDK |
|---|---|---|---|---|---|
| one-shot | `message.publish`, `clients.list`, `artifact.create/update/get/delete` | ✓ | ✓ | — | ✓ |
| pull-batch | `message.read` | ✓ | ✓ | — | ✓ |
| push-stream | `message.subscribe`, `artifact.watch` | ✓ | ✗ (as a tool) | ✓ | ✓ |

- **one-shot** and **pull-batch** verbs appear on every face.
- **push-stream** verbs are CLI/SDK-only as verbs; on Claude Code an agent
  receives them through the channel push, not as an MCP tool.
- `message.read` (pull) is the portable read path for every harness — and the
  catch-up path even where push exists, since a live stream only delivers from
  when it started.

## `methods.json`

The index is transport-neutral — no NATS operation appears here (that is the
[NATS binding](nats-binding.md)). Input/output keys name the parameters; types
are informal hints, with `lexicon` referencing the [Lexicons](lexicons.md).

```json
{{#include ../../../../protocol/methods.json}}
```
