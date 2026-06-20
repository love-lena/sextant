// The Client — a connected Sextant client. It mirrors the SEMANTICS of the Go
// SDK's Client (clients/go/sdk): publish/read/subscribe + the artifact CRUD and
// watch + the clients directory + inbox + principal + heartbeat. The TS surface
// is idiomatic — Promises for calls, a callback handler (returning a stop handle)
// for streams, and an async iterator for the inbox.
//
// Identity comes from the credential, not the caller (ADR-0012, ADR-0020): the
// id (a bus-minted ULID) and the display name are read from the JWT the bus
// authenticates, so what the client claims and what the bus authenticated cannot
// diverge — and the bus-stamped frame author is unforgeable.

import { readFile } from "node:fs/promises";
import { Events, type NatsConnection, type Subscription as NatsSub, type Msg } from "nats";
import {
  type ConnectOptions,
  BusError,
  call,
  dialNats,
  identityFromCreds,
  isUnknownOperation,
  newULID,
  resolveURL,
} from "./transport/conn.js";
import {
  OP,
  callSubject,
  deliverSubject,
  heartbeatSubject,
  DRAIN_SUB_ID,
} from "./transport/callsubjects.js";
import { clientSubject, MESSAGE_PREFIX } from "./transport/subjects.js";
import { decode } from "./wire/codec.js";
import { type Frame, validateFrame } from "./wire/frame.js";
import { EPOCH, DEFAULT_SKEW_TOLERANCE_MS, checkEpoch, checkSkew } from "./wire/epoch.js";
import type {
  JSONValue,
  Message,
  Artifact,
  ArtifactInfo,
  ArtifactChange,
  ClientInfo,
  IssuedClient,
  HeartbeatState,
} from "./types.js";

const DEFAULT_HEARTBEAT_INTERVAL_MS = 15_000;
const DEFAULT_HEARTBEAT_FRESHNESS_MS = 45_000;
const DEFAULT_REQUEST_TIMEOUT_MS = 30_000;
const INBOX_BUFFER = 64;

// ResumeDeferredError marks a non-fatal reconnect-resume notice: a resume failed
// on transport (the bus never answered), so the subscription stays registered
// and the next reconnect retries it. Distinguish it from a fatal resume failure
// (the bus answered that the resume is impossible). Mirrors Go's
// ErrResumeDeferred.
export class ResumeDeferredError extends Error {
  constructor(subject: string, cause: Error) {
    super(`sextant: subscription on ${JSON.stringify(subject)} resume deferred to the next reconnect: ${cause.message}`);
    this.name = "ResumeDeferredError";
  }
}

// SubOptions configures subscribe().
export interface SubOptions {
  deliverAll?: boolean; // replay the full backlog before live messages
  onError?: (e: Error) => void; // reconnect-resume notices (fatal + ResumeDeferredError)
}

// Subscription is an active message subscription; call stop() to end it
// (idempotent).
export interface Subscription {
  stop(): Promise<void>;
}

// Watch is an active artifact/principal watch; call stop() to end it
// (idempotent).
export interface Watch {
  stop(): Promise<void>;
}

// internalSub tracks one message subscription across reconnects: its current
// generation (subID + NATS subscription) rotates on every resume, while the
// subscription object is the stable identity.
interface InternalSub {
  subject: string;
  deliverAll: boolean;
  handler: (m: Message) => void;
  onError?: (e: Error) => void;
  subID: string;
  natsSub: NatsSub;
  lastSeq: number; // highest stream sequence delivered to the handler
  stopped: boolean;
}

// connect dials the bus and runs the connect handshake (mirror Go Connect →
// hello): authenticate with the client's own credential, hard-gate the protocol
// epoch via clients.hello, pre-subscribe the inbox + drain, and start the
// heartbeat loop. Returns a ready Client.
export async function connect(opts: ConnectOptions): Promise<Client> {
  if (!opts.credsPath) {
    throw new Error("sextant: no credentials (set credsPath; issue one with `sextant clients register <name>`)");
  }
  const { url } = await resolveURL(opts);
  const credsText = await readFile(opts.credsPath, "utf8");
  const identity = identityFromCreds(credsText);
  const nc = await dialNats(url, credsText, identity.id);
  const c = new Client(nc, identity.id, identity.displayName, opts);
  try {
    await c.hello();
    await c.subscribeDrain();
    await c.subscribeInbox();
    c.startHeartbeat();
    c.watchConnectionStatus();
  } catch (e) {
    await nc.close();
    throw e;
  }
  return c;
}

export class Client {
  private readonly nc: NatsConnection;
  private readonly _id: string;
  private readonly _displayName: string;
  private readonly log: (msg: string, ...a: unknown[]) => void;
  private readonly skewToleranceMs: number;
  private readonly heartbeatIntervalMs: number;
  private readonly heartbeatFreshnessMs: number;
  private readonly requestTimeoutMs: number;

  private _principal = "";
  private closed = false;

  // The active message subscriptions, re-established on reconnect.
  private readonly subs = new Set<InternalSub>();
  // Plain NATS subscriptions the SDK owns directly (inbox, drain, heartbeat
  // echo, artifact/principal watches) — torn down on close.
  private readonly ownedSubs = new Set<NatsSub>();

  // Drain signal: resolves when the bus broadcasts a cooperative drain.
  private drainResolve!: () => void;
  private readonly drainPromise: Promise<void>;
  private drainSignalled = false;

  // Inbox: a bounded queue feeding the inbox() async iterator (drop-oldest on
  // overflow, matching Go's non-blocking send).
  private readonly inboxQueue: Message[] = [];
  private inboxWaiter: ((m: IteratorResult<Message>) => void) | null = null;

  // Heartbeat state.
  private hbSeq = 0;
  private hbLastEchoSeq = 0;
  private hbLastEchoAt = new Date(0);
  private hbTimer: NodeJS.Timeout | null = null;

  constructor(nc: NatsConnection, id: string, displayName: string, opts: ConnectOptions) {
    this.nc = nc;
    this._id = id;
    this._displayName = displayName;
    this.log = opts.log ?? ((m, ...a) => console.error(m, ...a));
    this.skewToleranceMs = opts.skewToleranceMs ?? DEFAULT_SKEW_TOLERANCE_MS;
    this.heartbeatIntervalMs = opts.heartbeatIntervalMs ?? DEFAULT_HEARTBEAT_INTERVAL_MS;
    this.heartbeatFreshnessMs = opts.heartbeatFreshnessMs ?? DEFAULT_HEARTBEAT_FRESHNESS_MS;
    this.requestTimeoutMs = opts.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS;
    this.drainPromise = new Promise<void>((resolve) => {
      this.drainResolve = resolve;
    });
  }

  // --- identity / lifecycle ---

  // id is this client's identity: the bus-minted ULID (its registry key and
  // frame author).
  id(): string {
    return this._id;
  }

  // displayName is the human-readable label minted with the credential; may be "".
  displayName(): string {
    return this._displayName;
  }

  // principal is the bus-designated principal's ULID as last learned (at connect
  // and from any principal.watch delivery). "" means none designated.
  principal(): string {
    return this._principal;
  }

  // drained resolves when the bus broadcasts a cooperative drain. The standard
  // pattern awaits it and then calls close().
  drained(): Promise<void> {
    return this.drainPromise;
  }

  // close tears down all subscriptions + the heartbeat + the connection. It does
  // NOT retire the identity (ADR-0020): a clean close just drops presence to
  // offline; the durable identity persists.
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    if (this.hbTimer) clearInterval(this.hbTimer);
    // Stop the bus-side relays for the message subscriptions (idempotent), then
    // unsubscribe everything locally before closing the connection.
    for (const s of this.subs) {
      s.stopped = true;
      try {
        s.natsSub.unsubscribe();
      } catch {
        /* already gone */
      }
      await this.stopRelay(s.subID).catch(() => {});
    }
    this.subs.clear();
    for (const ns of this.ownedSubs) {
      try {
        ns.unsubscribe();
      } catch {
        /* already gone */
      }
    }
    this.ownedSubs.clear();
    // Wake any inbox iterator so a `for await` loop ends.
    if (this.inboxWaiter) {
      const w = this.inboxWaiter;
      this.inboxWaiter = null;
      w({ value: undefined, done: true });
    }
    await this.nc.close();
  }

  // --- the call envelope ---

  private call(op: string, input: JSONValue): Promise<JSONValue | undefined> {
    return call(this.nc, this._id, op, input, this.requestTimeoutMs);
  }

  // --- connect handshake (called by connect()) ---

  // hello is the connect handshake: a single clients.hello that confirms this is
  // a known identity, folds the protocol-epoch hard-gate (exact-match bus_epoch
  // === EPOCH; mismatch aborts the connect), soft-checks clock skew, and caches
  // the principal. Mirrors hello() in client.go.
  async hello(): Promise<void> {
    const out = (await this.call(OP.clientsHello, {})) as
      | { bus_epoch?: number; server_time?: string; principal?: string }
      | undefined;
    const busEpoch = out?.bus_epoch ?? 0;
    checkEpoch(busEpoch, EPOCH); // fatal on mismatch — abort the connect
    if (out?.server_time) {
      const t = Date.parse(out.server_time);
      if (!Number.isNaN(t)) {
        const skew = Math.abs(Date.now() - t);
        if (skew > this.skewToleranceMs) {
          this.log(
            `sextant: clock skew ${skew}ms vs the bus exceeds ${this.skewToleranceMs}ms; messages may be rejected — sync NTP`,
          );
        }
      }
    }
    this._principal = out?.principal ?? "";
  }

  // subscribeDrain pre-subscribes the cooperative-drain delivery
  // (sx.deliver.<id>.drain) so a drain broadcast right after connect is not
  // missed. Mirrors watchDrain in client.go.
  async subscribeDrain(): Promise<void> {
    const sub = this.nc.subscribe(deliverSubject(this._id, DRAIN_SUB_ID), {
      callback: () => {
        if (!this.drainSignalled) {
          this.drainSignalled = true;
          this.log("sextant: drain received; winding down");
          this.drainResolve();
        }
      },
    });
    this.ownedSubs.add(sub);
    await this.nc.flush();
  }

  // --- messages: publish ---

  // publish sends record to a msg.* subject (the bus stamps the frame). The
  // subject must be in the messages space.
  async publish(subject: string, record: JSONValue): Promise<void> {
    await this.publishMsg(subject, record);
  }

  // publishMsg is publish with the bus-stamped frame id and stream sequence
  // returned — for self-echo suppression.
  async publishMsg(subject: string, record: JSONValue): Promise<{ id: string; seq: number }> {
    if (!subject.startsWith(MESSAGE_PREFIX)) {
      throw new Error(`sextant: publish subject ${JSON.stringify(subject)} is not in the messages space (${MESSAGE_PREFIX}*)`);
    }
    const out = (await this.call(OP.messagePublish, { subject, record })) as
      | { id?: string; seq?: number }
      | undefined;
    return { id: out?.id ?? "", seq: Number(out?.seq ?? 0) };
  }

  // --- messages: pull (read) ---

  // fetchMessages pulls a batch of retained frames on subject from the cursor
  // since (0 = from the start), returning the frames and the next cursor.
  async fetchMessages(
    subject: string,
    since: number,
    limit: number,
  ): Promise<{ frames: Frame[]; next: number }> {
    const out = (await this.call(OP.messageRead, { subject, since, limit })) as
      | { messages?: JSONValue[]; next_cursor?: number }
      | undefined;
    const frames: Frame[] = [];
    for (const m of out?.messages ?? []) {
      frames.push(jsonToFrame(m));
    }
    return { frames, next: Number(out?.next_cursor ?? since) };
  }

  // --- messages: push (subscribe) ---

  // subscribe delivers messages matching subject to the handler. It pre-subscribes
  // the private delivery subject BEFORE issuing message.subscribe (race-free), and
  // survives reconnect by rotating the sub-id and resuming from lastSeq+1
  // (ADR-0027). Mirrors Subscribe in messages.go.
  async subscribe(
    subject: string,
    handler: (m: Message) => void,
    opts: SubOptions = {},
  ): Promise<Subscription> {
    const subID = newULID();
    const s: InternalSub = {
      subject,
      deliverAll: opts.deliverAll ?? false,
      handler,
      onError: opts.onError,
      subID,
      natsSub: this.nc.subscribe(deliverSubject(this._id, subID), {
        callback: (err, msg) => this.onDelivery(s, err, msg),
      }),
      lastSeq: 0,
      stopped: false,
    };
    this.subs.add(s);
    try {
      await this.call(OP.messageSubscribe, {
        subject,
        sub_id: subID,
        deliver_all: s.deliverAll,
      });
    } catch (e) {
      this.subs.delete(s);
      try {
        s.natsSub.unsubscribe();
      } catch {
        /* already gone */
      }
      throw e;
    }
    return {
      stop: async () => {
        if (s.stopped) return;
        s.stopped = true;
        this.subs.delete(s);
        try {
          s.natsSub.unsubscribe();
        } catch {
          /* already gone */
        }
        await this.stopRelay(s.subID).catch(() => {});
      },
    };
  }

  // onDelivery decodes a pushed MessageDelivery, applies the receiver-side
  // quarantine (validate + epoch + skew), drops overlap (monotonic cursor), and
  // hands a Message to the handler. Mirrors relayHandler + deliver in messages.go.
  private onDelivery(s: InternalSub, err: unknown, msg: Msg): void {
    if (err || s.stopped) return;
    let d: { sub_id?: string; subject?: string; seq?: number; bus_time?: string; frame?: JSONValue };
    try {
      d = JSON.parse(new TextDecoder().decode(msg.data));
    } catch (e) {
      this.log(`sextant: undecodable delivery on ${s.subject}, skipping: ${(e as Error).message}`);
      return;
    }
    if (d.frame === undefined) return;
    const frame = jsonToFrame(d.frame);
    const busTime = new Date(d.bus_time ?? 0);
    try {
      validateFrame(frame);
      checkEpoch(frame.epoch, EPOCH);
      checkSkew(frame.id, busTime, this.skewToleranceMs);
    } catch (e) {
      this.log(`sextant: quarantined frame on ${d.subject ?? s.subject}: ${(e as Error).message}`);
      return;
    }
    const seq = Number(d.seq ?? 0);
    if (seq > 0) {
      if (seq <= s.lastSeq) return; // overlap; drop
      s.lastSeq = seq;
    }
    s.handler({ frame, subject: d.subject ?? s.subject, busTime, sequence: seq });
  }

  // stopRelay ends a server-side relay (subscription.stop; idempotent). Used by
  // close, Subscription.stop, and the reconnect rotation.
  private async stopRelay(subID: string): Promise<void> {
    await this.call(OP.subscriptionStop, { sub_id: subID });
  }

  // --- reconnect resume (ADR-0027) ---

  // watchConnectionStatus drives the resume pass: on a NATS reconnect, every
  // active message subscription rotates to a fresh sub-id and resumes from
  // lastSeq+1, so no messages are missed or duplicated. A resume that fails
  // because the bus answered it is impossible is fatal (the subscription is
  // stopped); a transport failure is deferred to the next reconnect
  // (ResumeDeferredError). Mirrors the ReconnectHandler → reestablishSubs path.
  watchConnectionStatus(): void {
    void (async () => {
      for await (const status of this.nc.status()) {
        if (this.closed) return;
        if (status.type === Events.Reconnect) {
          await this.resumeAll();
        }
      }
    })();
  }

  private async resumeAll(): Promise<void> {
    for (const s of [...this.subs]) {
      if (this.closed || s.stopped) continue;
      try {
        await this.reestablish(s);
      } catch (e) {
        if (s.stopped) continue;
        if (e instanceof BusError) {
          // The bus answered that the resume is impossible (e.g. the log was
          // wiped): fatal — stop the subscription and notify loudly.
          this.log(`sextant: subscription on ${JSON.stringify(s.subject)} cannot resume after reconnect: ${e.message}`);
          s.stopped = true;
          this.subs.delete(s);
          try {
            s.natsSub.unsubscribe();
          } catch {
            /* already gone */
          }
          s.onError?.(new Error(`sextant: subscription on ${JSON.stringify(s.subject)} lost after reconnect: ${e.message}`));
          continue;
        }
        // Transport failure: stay registered, retry on the next reconnect.
        this.log(`sextant: subscription on ${JSON.stringify(s.subject)}: resume deferred to the next reconnect: ${(e as Error).message}`);
        s.onError?.(new ResumeDeferredError(s.subject, e as Error));
      }
    }
  }

  // reestablish replaces a subscription's relay generation after a reconnect: it
  // unsubscribes the old delivery subject, stops the old relay (idempotent),
  // subscribes a FRESH delivery subject, and re-issues message.subscribe carrying
  // the resume sequence (lastSeq+1). A zero lastSeq re-uses the original start
  // option. Mirrors reestablish in messages.go.
  private async reestablish(s: InternalSub): Promise<void> {
    const oldSubID = s.subID;
    try {
      s.natsSub.unsubscribe();
    } catch {
      /* already gone */
    }
    await this.stopRelay(oldSubID);

    const subID = newULID();
    s.subID = subID;
    s.natsSub = this.nc.subscribe(deliverSubject(this._id, subID), {
      callback: (err, msg) => this.onDelivery(s, err, msg),
    });
    const input: { subject: string; sub_id: string; deliver_all: boolean; since_seq?: number } = {
      subject: s.subject,
      sub_id: subID,
      deliver_all: s.deliverAll,
    };
    if (s.lastSeq > 0) {
      input.since_seq = s.lastSeq + 1;
      input.deliver_all = false; // since_seq takes priority over replay-from-top
    }
    await this.call(OP.messageSubscribe, input);
  }

  // --- artifacts: CRUD ---

  // createArtifact creates a new artifact from a record, returning the initial
  // revision (1). Fails if the name exists.
  async createArtifact(name: string, record: JSONValue): Promise<number> {
    const out = (await this.call(OP.artifactCreate, { name, record })) as { revision?: number } | undefined;
    return Number(out?.revision ?? 0);
  }

  // updateArtifact compare-and-set updates an artifact at expectedRev, returning
  // the new revision. Fails if the current revision differs (a concurrent write
  // moved it on) — the single-author discipline.
  async updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number> {
    const out = (await this.call(OP.artifactUpdate, { name, record, expected_rev: expectedRev })) as
      | { revision?: number }
      | undefined;
    return Number(out?.revision ?? 0);
  }

  // getArtifact reads an artifact's current value and bus-stamped metadata.
  async getArtifact(name: string): Promise<Artifact> {
    const out = (await this.call(OP.artifactGet, { name })) as
      | { name?: string; record?: JSONValue; revision?: number; createdAt?: string }
      | undefined;
    return {
      name: out?.name ?? name,
      record: out?.record ?? null,
      revision: Number(out?.revision ?? 0),
      created: parseTime(out?.createdAt),
    };
  }

  // deleteArtifact removes an artifact (unconditional).
  async deleteArtifact(name: string): Promise<void> {
    await this.call(OP.artifactDelete, { name });
  }

  // listArtifacts returns the artifacts directory — name + bus-stamped metadata,
  // sorted by name, no records (discovery, not contents).
  async listArtifacts(): Promise<ArtifactInfo[]> {
    const out = (await this.call(OP.artifactList, {})) as
      | { artifacts?: Array<{ name?: string; revision?: number; createdAt?: string; updatedAt?: string }> }
      | undefined;
    return (out?.artifacts ?? []).map((e) => ({
      name: e.name ?? "",
      revision: Number(e.revision ?? 0),
      created: parseTime(e.createdAt),
      updated: parseTime(e.updatedAt),
    }));
  }

  // --- artifacts: watch ---

  // watchArtifact calls the handler on each change to name: the current value
  // first (if present), then each write and delete. Same pre-subscribe-then-call
  // discipline as subscribe.
  async watchArtifact(name: string, handler: (c: ArtifactChange) => void): Promise<Watch> {
    const subID = newULID();
    const natsSub = this.nc.subscribe(deliverSubject(this._id, subID), {
      callback: (err, msg) => {
        if (err) return;
        let d: {
          name?: string;
          record?: JSONValue;
          revision?: number;
          createdAt?: string;
          deleted?: boolean;
        };
        try {
          d = JSON.parse(new TextDecoder().decode(msg.data));
        } catch (e) {
          this.log(`sextant: undecodable artifact delivery for ${name}, skipping: ${(e as Error).message}`);
          return;
        }
        handler({
          name: d.name ?? name,
          record: d.deleted ? null : (d.record ?? null),
          revision: Number(d.revision ?? 0),
          created: d.deleted ? new Date(0) : parseTime(d.createdAt),
          deleted: d.deleted ?? false,
        });
      },
    });
    this.ownedSubs.add(natsSub);
    try {
      await this.call(OP.artifactWatch, { name, sub_id: subID });
    } catch (e) {
      this.ownedSubs.delete(natsSub);
      try {
        natsSub.unsubscribe();
      } catch {
        /* already gone */
      }
      throw e;
    }
    return this.makeWatch(natsSub, subID);
  }

  // --- clients directory + mint-on-behalf ---

  // listClients returns the clients directory: every issued identity, online and
  // offline (ADR-0020).
  async listClients(): Promise<ClientInfo[]> {
    return listClientsVia((op, input) => this.call(op, input));
  }

  // register asks the bus to mint a NEW child identity over THIS client's
  // connection — mint-on-behalf (ADR-0033), authorized only for a dispatcher; the
  // bus forces kind=agent. The returned creds are secret material.
  async register(displayName: string, kind: string): Promise<IssuedClient> {
    const out = (await this.call(OP.clientsRegister, { display_name: displayName, kind })) as
      | { id?: string; creds?: string }
      | undefined;
    return { id: out?.id ?? "", creds: out?.creds ?? "" };
  }

  // --- inbox ---

  // inbox is an async iterator over messages published to this client's own inbox
  // subject (msg.client.<id>), auto-subscribed at connect. The queue is bounded
  // (cap 64); a slow consumer drops the oldest, matching Go's non-blocking send.
  inbox(): AsyncIterableIterator<Message> {
    const self = this;
    return {
      [Symbol.asyncIterator]() {
        return this;
      },
      next(): Promise<IteratorResult<Message>> {
        if (self.inboxQueue.length > 0) {
          return Promise.resolve({ value: self.inboxQueue.shift()!, done: false });
        }
        if (self.closed) {
          return Promise.resolve({ value: undefined, done: true });
        }
        return new Promise<IteratorResult<Message>>((resolve) => {
          self.inboxWaiter = resolve;
        });
      },
    };
  }

  // subscribeInbox sets up the auto-subscription to msg.client.<self> so the
  // client is reachable by direct message the instant it connects (ADR-0030). It
  // is an ordinary server-side relay (the same message.subscribe path an explicit
  // subscribe uses), feeding the bounded inbox queue. Mirrors subscribeInbox in
  // client.go (which reuses Subscribe). Registered in this.subs so it is
  // re-established on reconnect like any other subscription.
  async subscribeInbox(): Promise<void> {
    await this.subscribe(clientSubject(this._id), (m) => this.enqueueInbox(m));
  }

  private enqueueInbox(m: Message): void {
    if (this.inboxWaiter) {
      const w = this.inboxWaiter;
      this.inboxWaiter = null;
      w({ value: m, done: false });
      return;
    }
    this.inboxQueue.push(m);
    if (this.inboxQueue.length > INBOX_BUFFER) {
      this.inboxQueue.shift(); // drop-oldest on overflow
      this.log(`sextant: inbox buffer full; dropping a message`);
    }
  }

  // --- principal (ADR-0030) ---

  // getPrincipal reads the current principal ULID and advances the cache.
  async getPrincipal(): Promise<string> {
    const out = (await this.call(OP.principalGet, {})) as { principal?: string } | undefined;
    this._principal = out?.principal ?? "";
    return this._principal;
  }

  // watchPrincipal calls the handler on each principal designation (current value
  // first), keeping principal() current for the life of the watch.
  async watchPrincipal(handler: (principal: string) => void): Promise<Watch> {
    const subID = newULID();
    const natsSub = this.nc.subscribe(deliverSubject(this._id, subID), {
      callback: (err, msg) => {
        if (err) return;
        let d: { principal?: string };
        try {
          d = JSON.parse(new TextDecoder().decode(msg.data));
        } catch {
          return;
        }
        this._principal = d.principal ?? "";
        handler(this._principal);
      },
    });
    this.ownedSubs.add(natsSub);
    try {
      await this.call(OP.principalWatch, { sub_id: subID });
    } catch (e) {
      this.ownedSubs.delete(natsSub);
      try {
        natsSub.unsubscribe();
      } catch {
        /* already gone */
      }
      throw e;
    }
    return this.makeWatch(natsSub, subID);
  }

  // --- heartbeat (TASK-126) ---

  // heartbeatState returns a snapshot of the heartbeat round-trip state.
  heartbeatState(): HeartbeatState {
    const fresh =
      this.hbLastEchoAt.getTime() !== 0 && Date.now() - this.hbLastEchoAt.getTime() < this.heartbeatFreshnessMs;
    return {
      lastBeatSeq: this.hbSeq,
      lastEchoSeq: this.hbLastEchoSeq,
      lastEchoAt: this.hbLastEchoAt,
      fresh,
    };
  }

  // startHeartbeat wires the echo watcher and the beat loop. A bus that does not
  // implement clients.heartbeat answers "unknown operation"; the loop then stops
  // (presence falls back to the connection table), never crashing. Mirrors
  // startHeartbeat in heartbeat.go.
  startHeartbeat(): void {
    const echoSub = this.nc.subscribe(heartbeatSubject(this._id), {
      callback: (err, msg) => {
        if (err) return;
        try {
          const echo = JSON.parse(new TextDecoder().decode(msg.data)) as { seq?: number };
          this.hbLastEchoSeq = Number(echo.seq ?? 0);
          this.hbLastEchoAt = new Date();
        } catch {
          /* undecodable echo */
        }
      },
    });
    this.ownedSubs.add(echoSub);
    this.hbTimer = setInterval(() => {
      void this.beat();
    }, this.heartbeatIntervalMs);
    // Don't keep the event loop alive solely for heartbeats.
    this.hbTimer.unref?.();
  }

  private async beat(): Promise<void> {
    if (this.closed) return;
    this.hbSeq++;
    try {
      await this.call(OP.clientsHeartbeat, { seq: this.hbSeq });
    } catch (e) {
      if (isUnknownOperation(e)) {
        this.log("sextant: bus does not implement clients.heartbeat; stopping heartbeats (presence falls back to the connection table)");
        if (this.hbTimer) clearInterval(this.hbTimer);
        return;
      }
      this.log(`sextant: heartbeat failed (will retry next interval): ${(e as Error).message}`);
    }
  }

  // --- shared helpers ---

  private makeWatch(natsSub: NatsSub, subID: string): Watch {
    let stopped = false;
    return {
      stop: async () => {
        if (stopped) return;
        stopped = true;
        try {
          natsSub.unsubscribe();
        } catch {
          /* already gone */
        }
        this.ownedSubs.delete(natsSub);
        await this.stopRelay(subID).catch(() => {});
      },
    };
  }
}

// listClientsVia is the shared clients.list mapping behind Client.listClients and
// Issuer.listClients (both make the same call, differing only in the connection).
export async function listClientsVia(
  caller: (op: string, input: JSONValue) => Promise<JSONValue | undefined>,
): Promise<ClientInfo[]> {
  const out = (await caller(OP.clientsList, {})) as
    | { clients?: Array<{ id?: string; display_name?: string; kind?: string; epoch?: number; presence?: string; issued_at?: string }> }
    | undefined;
  const infos: ClientInfo[] = [];
  for (const e of out?.clients ?? []) {
    const issuedAt = parseTime(e.issued_at);
    if (Number.isNaN(issuedAt.getTime())) continue; // the bus owns these; skip a bad timestamp
    infos.push({
      id: e.id ?? "",
      displayName: e.display_name ?? "",
      kind: e.kind ?? "",
      epoch: Number(e.epoch ?? 0),
      online: e.presence === "online",
      issuedAt,
    });
  }
  return infos;
}

// jsonToFrame maps a decoded JSON object (a frame as the bus delivers it inside a
// Response or delivery) onto the Frame shape. Unlike the codec's decode (which
// works from canonical bytes), this works from an already-parsed value — the
// path the read/subscribe replies take. It does not validate; the receive path
// validates separately.
function jsonToFrame(v: JSONValue): Frame {
  if (v === null || typeof v !== "object" || Array.isArray(v)) {
    throw new Error("sextant: frame is not a JSON object");
  }
  const o = v as { [k: string]: JSONValue };
  const f: Frame = {
    id: typeof o["id"] === "string" ? o["id"] : "",
    author: typeof o["author"] === "string" ? o["author"] : "",
    kind: typeof o["kind"] === "string" ? o["kind"] : "",
    epoch: typeof o["epoch"] === "number" ? o["epoch"] : Number(o["epoch"] ?? 0),
    record: o["record"] ?? null,
  };
  if (o["revision"] !== undefined) f.revision = Number(o["revision"]);
  if (typeof o["createdAt"] === "string") f.createdAt = o["createdAt"];
  if (typeof o["updatedAt"] === "string") f.updatedAt = o["updatedAt"];
  return f;
}

// parseTime parses a bus RFC3339 timestamp, returning the epoch (zero) Date when
// it is empty or unparseable (a missing time is not worth failing a read).
function parseTime(s: string | undefined): Date {
  if (!s) return new Date(0);
  const t = Date.parse(s);
  return Number.isNaN(t) ? new Date(0) : new Date(t);
}

// re-export the codec entry point a caller may want for raw frames.
export { decode };
