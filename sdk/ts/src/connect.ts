// The Node entry's connect (ADR-0044): it reads the .creds file and the bus.json
// discovery file (the Node-only sites), then hands off to the transport-agnostic
// connectCore in client.ts. It lives in its own module — not client.ts — so the
// browser bundle, which imports Client + connectCore from client.ts, pulls in NO
// node:* and no TCP `nats` (those enter only here, the Node path). opts.dial
// defaults to the TCP dialNats, so the Node SDK's behaviour is byte-for-byte
// unchanged.

import { type ConnectOptions } from "./transport/conn.js";
import { dialNats, resolveURL, readCredsFile } from "./transport/node.js";
import { connectCore, type Client } from "./client.js";

// connect dials the bus and runs the connect handshake (mirror Go Connect →
// hello): authenticate with the client's own credential, hard-gate the protocol
// epoch via clients.hello, pre-subscribe the inbox + drain, and start the
// heartbeat loop. Returns a ready Client.
export async function connect(opts: ConnectOptions): Promise<Client> {
  if (!opts.credsPath) {
    throw new Error("sextant: no credentials (set credsPath; issue one with `sextant clients register <name>`)");
  }
  const { url } = await resolveURL(opts);
  const credsText = await readCredsFile(opts.credsPath);
  return connectCore(url, credsText, opts.dial ?? dialNats, opts);
}
