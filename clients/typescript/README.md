# @sextant/client

TypeScript client for the sextant bus. Mirrors the Go
[`pkg/client`](../../pkg/client) one-to-one.

Canonical API surface:
[`specs/components/client-libraries.md`](../../specs/components/client-libraries.md).

## Quickstart

```ts
import { connect } from "@sextant/client";

const client = await connect(); // reads ~/.config/sextant/client.toml

// Subscribe
for await (const msg of client.subscribe("agents.*.frames")) {
  if (msg.err) {
    console.error("bad envelope on", msg.subject, msg.err);
    continue;
  }
  console.log(msg.envelope?.kind, msg.envelope?.id);
  await msg.ack();
}

// RPC
import type { ListAgentsRequest, ListAgentsResponse } from "@sextant/client";
const { agents } = await client.rpc<ListAgentsRequest, ListAgentsResponse>(
  "list_agents",
  {},
);

await client.close();
```

## Development

```bash
npm install
npm run codegen     # regenerate src/types.generated.ts from pkg/sextantproto/schemas/
npm run lint        # tsc --noEmit
npm test            # vitest (spawns nats-server, full integration)
npm run build       # emit dist/
```

The integration test spawns `nats-server` from `$PATH`, so it must be
on the machine — `brew install nats-server` on macOS, or `apt install
nats-server` on Linux CI runners.

For the M8 acceptance against a real `sextantd`, see
[`scripts/acceptance.ts`](scripts/acceptance.ts).
