// The Node-only transport sites (ADR-0044): the TCP dialer over `nats` and the
// filesystem reads (bus.json discovery, the .creds file). They are split out of
// the shared conn.ts so the browser entry pulls in no `node:*` and no TCP `nats`
// — the browser is handed its credsText + ws url and dials over `nats.ws`. The
// Node SDK entry (index.ts) wires dialNats as its default Dialer, so its
// behaviour is byte-for-byte unchanged.

import { readFile } from "node:fs/promises";
import { connect as natsConnect, credsAuthenticator, type NatsConnection } from "nats";
import { dialConnectOptions, type ConnectOptions } from "./conn.js";

// resolveURL determines the bus URL: an explicit url wins, otherwise it reads the
// bus.json discovery file. Returns the URL and the discovery path used (empty when
// the URL was pinned). Node-only — it reads the filesystem.
export async function resolveURL(opts: ConnectOptions): Promise<{ url: string; connInfoPath: string }> {
  if (opts.url) {
    return { url: opts.url, connInfoPath: "" };
  }
  if (opts.connInfoPath) {
    const url = await readBusURL(opts.connInfoPath);
    return { url, connInfoPath: opts.connInfoPath };
  }
  throw new Error("sextant: no bus URL (set url or connInfoPath)");
}

// readBusURL reads the url field from a bus.json discovery file. Mirrors
// conninfo.Read. Node-only.
export async function readBusURL(connInfoPath: string): Promise<string> {
  const text = await readFile(connInfoPath, "utf8");
  let info: { url?: string };
  try {
    info = JSON.parse(text);
  } catch (e) {
    throw new Error(`sextant: parse ${connInfoPath}: ${(e as Error).message}`);
  }
  if (!info.url) {
    throw new Error(`sextant: ${connInfoPath} carries no url`);
  }
  return info.url;
}

// dialNats is the Node Dialer: a TCP `nats` connection with creds auth on top of
// the shared dialConnectOptions. It is the default dial for the Node SDK entry.
export async function dialNats(url: string, credsText: string, id: string): Promise<NatsConnection> {
  return natsConnect({
    ...dialConnectOptions(url, id),
    authenticator: credsAuthenticator(new TextEncoder().encode(credsText)),
  });
}

// readCredsFile reads a .creds file's text. Node-only; the browser is handed
// credsText directly (it never touches the filesystem).
export async function readCredsFile(credsPath: string): Promise<string> {
  return readFile(credsPath, "utf8");
}
