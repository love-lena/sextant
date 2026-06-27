// BusConnection owns the SDK client lifecycle for one pi session: the idempotent
// open (the spike's adjustment 1), the inbox + watch-topic subscriptions that
// drive the wake, the trust tiers learned from the live bus, and the clean
// drain+close. The extension (index.ts) drives it from pi's session_start /
// session_shutdown, and the tools/command resolve the live client through it.
//
// Idempotency is the load-bearing property here. pi fires session_start (reason
// "new") TWICE for a single new_session in RPC mode (reproduced in the spike with
// a trivial extension, no bus). A naive open-on-session_start LEAKS the first
// client on the second fire — and worse, the second fire tears down the client
// the first just subscribed, a window where an inbound publish is missed. So
// open() is close-before-open: it disposes any held client first. open() also
// serialises against itself (an in-flight open is awaited) so two near-
// simultaneous fires cannot both dial.

import {
  connect,
  type Client,
  type Message,
  type Subscription,
} from "@sextant/sdk";
import { clientSubject, topicSubject } from "@sextant/sdk";
import type { Config } from "./config.js";
import type { Tiers } from "./trust.js";

// Logger is the trace seam — one structured line per lifecycle event. stderr in
// production (RPC stdout must stay clean JSONL); a test can capture it.
export type Logger = (event: string, fields?: Record<string, unknown>) => void;

export class BusConnection {
  private client: Client | undefined;
  private opening: Promise<void> | undefined; // in-flight open, to serialise double-fire
  private generation = 0; // bumped each open; lets a stale open detect it lost a race
  // Runtime subscriptions opened by sextant_subscribe, plus the inbox + watch
  // subs, tracked so close() stops them all.
  private readonly subscriptions = new Map<string, Subscription>();
  private tiers: Tiers = { principalId: "", selfId: "", knownPeerIds: new Set() };

  constructor(
    private readonly cfg: Config,
    private readonly log: Logger,
    // onWake is the extension's wake handler; every inbox/watch/runtime frame
    // flows through it (so the back-pressure policy sees them all).
    private readonly onWake: (m: Message) => void,
  ) {}

  getClient(): Client | undefined {
    return this.client;
  }

  getTiers(): Tiers {
    return this.tiers;
  }

  runtimeSubscriptions(): Map<string, Subscription> {
    return this.subscriptions;
  }

  // activitySubject is the per-agent activity stream: msg.agent.<id>.activity
  // (entity.id.aspect, parallels msg.workflow.<id>.events). An explicit
  // SEXTANT_ACTIVITY_TOPIC override still wins (wrapped as a plain topic).
  activitySubject(): string {
    if (this.cfg.activityTopic) return topicSubject(this.cfg.activityTopic);
    return "msg.agent." + (this.client?.id() ?? "") + ".activity";
  }

  // handoffSubject is the bus subject the managed handoff (TASK-178) announces
  // relinquished / acquired on, so the dash + the dispatcher see ownership move.
  handoffSubject(): string {
    return topicSubject(this.cfg.handoffTopic);
  }

  // open connects the client and wires the inbox + watch subscriptions. It is
  // IDEMPOTENT and self-serialising: a held client is closed first; a concurrent
  // open is awaited rather than racing a second dial. Safe to call on every
  // session_start fire.
  async open(reason: string): Promise<void> {
    // Serialise: if an open is already running, wait for it (the double-fire
    // case) instead of starting a second dial.
    if (this.opening) {
      this.log("open_awaiting_inflight", { reason });
      await this.opening.catch(() => {});
    }
    let release!: () => void;
    this.opening = new Promise<void>((r) => (release = r));
    try {
      await this.doOpen(reason);
    } finally {
      release();
      this.opening = undefined;
    }
  }

  private async doOpen(reason: string): Promise<void> {
    // Close-before-open: dispose any client we already hold so the second
    // session_start fire cannot leak the first one's connection.
    if (this.client) {
      this.log("reopen_close_prior", { reason });
      await this.closeClient();
    }

    if (!this.cfg.credsPath) {
      this.log("config_error", { detail: "SEXTANT_PI_CREDS is required; staying dormant (not bus-connected)" });
      return;
    }

    const gen = ++this.generation;
    let client: Client;
    try {
      client = await connect({
        credsPath: this.cfg.credsPath,
        url: this.cfg.busURL || undefined,
        connInfoPath: this.cfg.busJSONPath || undefined,
        log: (msg) => this.log("sdk_log", { msg }),
      });
    } catch (e) {
      this.log("connect_error", { detail: (e as Error).message });
      return;
    }

    // A newer open() started while we were dialing (a rapid double-fire): this
    // client is stale. Close it and let the newer one stand.
    if (gen !== this.generation) {
      this.log("stale_open_discarded", { gen, current: this.generation });
      await client.close().catch(() => {});
      return;
    }

    this.client = client;
    this.log("connected", { id: client.id(), displayName: client.displayName(), reason });

    // Learn the trust tiers from the live bus: the principal (ADR-0030) and the
    // directory-known identities (verified peers). Best-effort — a failure leaves
    // the tiers at "only self trusted", which is the safe default.
    await this.learnTiers(client);

    // Subscribe the inbox (direct address) and the configured watch topics. The
    // SDK auto-subscribes the inbox for its own inbox() iterator, but we want the
    // wake path, so we subscribe the inbox subject explicitly here too.
    await this.subscribeWake(clientSubject(client.id()), "inbox");
    for (const t of this.cfg.watchTopics) {
      await this.subscribeWake(topicSubject(t), `watch:${t}`);
    }
  }

  private async learnTiers(client: Client): Promise<void> {
    const selfId = client.id();
    let principalId = client.principal();
    const knownPeerIds = new Set<string>();
    try {
      if (!principalId) principalId = await client.getPrincipal();
    } catch {
      /* best-effort */
    }
    try {
      for (const info of await client.listClients()) {
        if (info.id && info.id !== selfId) knownPeerIds.add(info.id);
      }
    } catch (e) {
      this.log("list_clients_error", { detail: (e as Error).message });
    }
    this.tiers = { principalId, selfId, knownPeerIds };
    this.log("tiers", { principal: principalId, peers: knownPeerIds.size });
  }

  private async subscribeWake(subject: string, label: string): Promise<void> {
    try {
      const sub = await this.client!.subscribe(subject, this.onWake);
      this.subscriptions.set(subject, sub);
      this.log("subscribed", { subject, label });
    } catch (e) {
      this.log("subscribe_error", { subject, label, detail: (e as Error).message });
    }
  }

  // close drains and tears down the client + all subscriptions. Called from
  // session_shutdown. Idempotent.
  async close(reason: string): Promise<void> {
    this.log("close", { reason });
    await this.closeClient();
  }

  private async closeClient(): Promise<void> {
    for (const [subject, sub] of this.subscriptions) {
      try {
        await sub.stop();
      } catch {
        /* already gone */
      }
      this.subscriptions.delete(subject);
    }
    if (this.client) {
      try {
        await this.client.close();
        this.log("closed");
      } catch (e) {
        this.log("close_error", { detail: (e as Error).message });
      }
      this.client = undefined;
    }
    this.tiers = { principalId: "", selfId: "", knownPeerIds: new Set() };
  }
}
