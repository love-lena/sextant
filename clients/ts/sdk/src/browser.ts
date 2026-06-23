// @sextant/sdk/browser — the browser entry of the TypeScript Wire client
// (ADR-0044). It is the SAME Client as the Node entry, only the transport differs:
// it dials the bus's loopback WebSocket listener over `nats.ws`/wss instead of
// `nats`/TCP, and it is handed its credential TEXT + ws URL (the dash mints a
// short-lived scoped credential and serves it to the page) rather than reading the
// filesystem. So a browser becomes a co-equal bus client: it runs the conventions
// (@sextant/conv-goals, @sextant/conv-review) directly over its own Client, with
// no Go-backend convention re-implementation in between.
//
// Everything the Node entry exports is re-exported here so a browser bundle has
// one import surface; the only addition is browserConnect.

import { connect as natsWsConnect, credsAuthenticator } from "nats.ws";
import type { NatsConnection } from "nats";
import { connectCore, Client } from "./client.js";
import {
  type CoreConnectOptions,
  type Dialer,
  dialConnectOptions,
  identityFromCreds,
} from "./transport/conn.js";

// BrowserConnectOptions configures browserConnect. Unlike the Node ConnectOptions
// it takes the ws url and the credential TEXT directly — there is no credsPath and
// no connInfoPath, because a browser has no filesystem: the dash hands the page
// both (POST /api/session → {creds, wsURL}). The tunables match the Node options.
export interface BrowserConnectOptions extends CoreConnectOptions {
  url: string; // ws://127.0.0.1:<port> — the bus WebSocket listener (required)
  credsText: string; // the .creds text the dash minted for this tab (required)
}

// wsDialer is the browser Dialer: a nats.ws WebSocket connection with creds auth
// on top of the shared dialConnectOptions. It is the browser peer of dialNats —
// same options, the only difference is the import source (nats.ws vs nats), which
// is exactly the transport seam ADR-0044 introduces. The nats.ws NatsConnection is
// structurally the same shape as nats's (Synadia's two clients share the protocol
// layer); the cast bridges the two nominal type sets at the one seam.
const wsDialer: Dialer = async (url, credsText, id): Promise<NatsConnection> => {
  const nc = await natsWsConnect({
    ...dialConnectOptions(url, id),
    authenticator: credsAuthenticator(new TextEncoder().encode(credsText)),
  });
  return nc as unknown as NatsConnection;
};

// browserConnect dials the bus over wss and runs the same connect handshake the
// Node SDK runs (hello → drain → inbox → heartbeat), returning a ready Client. The
// credential is read from credsText (never the filesystem); the identity is the
// JWT name claim the bus authenticates, exactly as on the Node path.
export async function browserConnect(opts: BrowserConnectOptions): Promise<Client> {
  if (!opts.url) {
    throw new Error("sextant: no bus WebSocket URL (set url to ws://host:port)");
  }
  if (!opts.credsText) {
    throw new Error("sextant: no credentials (set credsText — the dash mints one via POST /api/session)");
  }
  return connectCore(opts.url, opts.credsText, wsDialer, opts);
}

// identityFromCreds is re-exported so a page can read its own id/displayName from
// the minted credential without connecting (e.g. to render "you are X" before the
// WS is up).
export { identityFromCreds };

// Re-export the full public surface so @sextant/sdk/browser is a superset of
// @sextant/sdk — a browser bundle imports everything from one place.
export { Client, ResumeDeferredError } from "./client.js";
export type { SubOptions, Subscription, Watch } from "./client.js";
export type { ConnectOptions, Dialer, CoreConnectOptions } from "./transport/conn.js";
export { BusError } from "./transport/conn.js";
export { EPOCH, EpochError, SkewError, checkEpoch, checkSkew } from "./wire/epoch.js";
export {
  type Frame,
  KIND_MESSAGE,
  KIND_ARTIFACT,
  validateFrame,
  isValidULID,
  parseULIDMillis,
} from "./wire/frame.js";
export { canonical, encode, encodeHex, decode, decodeHex, parseJSON, bytesToHex, hexToBytes } from "./wire/codec.js";
export { topicSubject, clientSubject, dmSubject, MESSAGE_PREFIX } from "./transport/subjects.js";
export type {
  JSONValue,
  Message,
  Artifact,
  ArtifactInfo,
  ArtifactChange,
  ClientInfo,
  IssuedClient,
  HeartbeatState,
} from "./types.js";
