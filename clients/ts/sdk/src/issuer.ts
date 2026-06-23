// The Issuer — a connection used to issue and retire identities (ADR-0020): the
// held-identity (operator) or bootstrap/enrollment authority. It is NOT a full
// client: it runs no connect handshake (no clients.hello, no drain watch) and
// holds no durable identity of its own — its job is to mint and decommission
// OTHER identities. Mirrors the Go SDK's Issuer (clients/go/sdk/issuer.go).
//
// This is the documented scoped-creds path (AC#5): a TS process that needs its
// own bus identity connects an Issuer with operator.creds (or enroll.creds),
// calls register({display_name, kind}) → {id, creds}, writes the creds to a 0600
// file, and then connect()s as that minted identity — never the operator's
// ambient creds.

import type { NatsConnection } from "nats";
import {
  type ConnectOptions,
  call,
  identityFromCreds,
} from "./transport/conn.js";
import { dialNats, resolveURL, readCredsFile } from "./transport/node.js";
import { OP } from "./transport/callsubjects.js";
import { listClientsVia } from "./client.js";
import type { JSONValue, ClientInfo, IssuedClient } from "./types.js";

const DEFAULT_REQUEST_TIMEOUT_MS = 30_000;

// connectIssuer dials the bus with an issuer credential (operator or enrollment).
// Unlike connect() it performs no handshake — the issuer is not a directory
// client — so it works for the enrollment identity (whose allow-list permits only
// clients.register) and for the operator before any client identity exists.
export async function connectIssuer(opts: ConnectOptions): Promise<Issuer> {
  if (!opts.credsPath) {
    throw new Error("sextant: no issuer credentials (set credsPath)");
  }
  const { url } = await resolveURL(opts);
  const credsText = await readCredsFile(opts.credsPath);
  // The issuer's id is the reserved name inside its credential (operator/enroll);
  // it is the call-subject token the bus authorizes the issuance path against.
  const identity = identityFromCreds(credsText);
  const nc = await dialNats(url, credsText, identity.id);
  return new Issuer(nc, identity.id, opts.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS);
}

export class Issuer {
  constructor(
    private readonly nc: NatsConnection,
    private readonly issuerID: string,
    private readonly requestTimeoutMs: number,
  ) {}

  private call(op: string, input: JSONValue): Promise<JSONValue | undefined> {
    return call(this.nc, this.issuerID, op, input, this.requestTimeoutMs);
  }

  // register mints a NEW identity with the given display name and kind, returning
  // its id and credential. The signing keys never leave the bus. Authorization is
  // the issuer's own authority (operator = held-identity, enroll = bootstrap),
  // enforced by the bus.
  async register(displayName: string, kind: string): Promise<IssuedClient> {
    const out = (await this.call(OP.clientsRegister, { display_name: displayName, kind })) as
      | { id?: string; creds?: string }
      | undefined;
    return { id: out?.id ?? "", creds: out?.creds ?? "" };
  }

  // retire decommissions an identity for good (operator-only, enforced by the
  // bus). Distinct from a disconnect, which only goes offline.
  async retire(id: string): Promise<void> {
    await this.call(OP.clientsRetire, { id });
  }

  // listClients returns the directory, for an issuer authorized to read it (the
  // operator).
  async listClients(): Promise<ClientInfo[]> {
    return listClientsVia((op, input) => this.call(op, input));
  }

  // setPrincipal points the bus's principal designation at a client ULID
  // (ADR-0030, ADR-0031). force authorizes re-pointing an already-established
  // principal (ignored on a first claim). The bus enforces the asymmetry.
  async setPrincipal(principal: string, force: boolean): Promise<void> {
    await this.call(OP.principalSet, { principal, force });
  }

  // getPrincipal reads the current principal ULID over the issuer connection.
  async getPrincipal(): Promise<string> {
    const out = (await this.call(OP.principalGet, {})) as { principal?: string } | undefined;
    return out?.principal ?? "";
  }

  // close closes the issuer connection.
  async close(): Promise<void> {
    await this.nc.close();
  }
}
