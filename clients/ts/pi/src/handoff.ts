// The managed close-and-resume handoff (TASK-178, AC#3) — the spike's untested
// SECONDARY path, built here. A headless pi worker runs as a single owner of its
// pi session (its JSONL). An owner that wants to release the worker — to resume it
// by hand, hand it to another box, or re-spawn it fresh — sends a pi.handoff
// {verb:"drain"} to the worker's inbox. The worker cooperatively winds down and
// exits; the operator resumes the persisted session by hand; the dispatcher
// re-spawns it under the SAME session id. Nothing fights the session because the
// relinquish completes (worker offline) before any re-spawn acquires it.
//
// Why control is intercepted, not woken: a pi.handoff is CONTROL, not a task. If a
// drain frame flowed through the normal wake path the model would "see" it as work
// to do. So the extension routes a recognised pi.handoff to onDrain() and returns
// WITHOUT enqueuing a wake — the same shape as the goals convention (a structured
// record the client acts on directly), kept out of the agent loop.
//
// Why cooperative, not a kill: like workflow.control, a handoff ASKS. The worker
// finishes its current turn (no half-done turn lost), persists the session JSONL
// pi already maintains, announces it relinquished the named session, drains+closes
// its bus client (so presence drops to offline — the visible single-owner release),
// then calls ctx.shutdown() to exit the process. Force-stopping a wedged worker is
// the OS's job via the dispatcher that launched it, exactly as ADR-0011 frames a
// wedged workflow coordinator.
//
// The sequencing is pure and unit-tested (isHandoff + the wind-down ordering over a
// small Deps seam); the extension supplies the live pi/bus side effects.

import type { JSONValue } from "@sextant/sdk";

// HANDOFF_TYPE is the $type a control frame carries to be recognised as a handoff.
export const HANDOFF_TYPE = "pi.handoff";

// Verbs. drain is sent TO the worker (wind down). relinquished / acquired are sent
// BY the worker (announcements the dash + dispatcher read), never acted on by it.
export const VerbDrain = "drain";
export const VerbRelinquished = "relinquished";
export const VerbAcquired = "acquired";

// HandoffRecord is the pi.handoff lexicon record (protocol/lexicons/pi.handoff.json).
export interface HandoffRecord {
  $type: typeof HANDOFF_TYPE;
  verb: string;
  session?: string;
  reason?: string;
  updated?: string;
}

// isHandoffDrain reports whether an opaque frame record is a pi.handoff DRAIN
// request — the only verb a worker acts on. relinquished/acquired are the worker's
// own announcements (or another worker's), never a command to wind down, so they
// are NOT treated as a drain even though they share the $type. Pure + total: any
// shape that is not exactly a drain returns false (it falls through to a normal
// wake, so a malformed control frame is never silently swallowed).
export function isHandoffDrain(record: JSONValue): boolean {
  if (!record || typeof record !== "object" || Array.isArray(record)) return false;
  const r = record as { $type?: unknown; verb?: unknown };
  return r.$type === HANDOFF_TYPE && r.verb === VerbDrain;
}

// HandoffDeps is the seam the wind-down drives, so the ordering is testable without
// pi or a bus. The extension supplies the live implementations.
export interface HandoffDeps {
  // sessionId resolves the pi session id being relinquished (for the announcement
  // and the operator's resume). "" if pi has not assigned one yet.
  sessionId: () => string;
  // isIdle reports whether the agent has finished its current turn (so a drain
  // never truncates a turn mid-flight).
  isIdle: () => boolean;
  // announce publishes a pi.handoff announcement (relinquished / acquired) on the
  // bus, so the ownership transfer is visible. Best-effort; a failure must not
  // block the wind-down (the close + exit are what actually release the session).
  announce: (rec: HandoffRecord) => Promise<void>;
  // closeBus drains + closes the worker's SDK client (presence → offline). This is
  // the visible single-owner RELEASE: until it completes the worker still owns the
  // session, so a re-spawn must wait for it.
  closeBus: () => Promise<void>;
  // exit gracefully stops the pi process (ctx.shutdown()); the session JSONL pi
  // maintains is already persisted, so the next spawn can resume it.
  exit: () => void;
  // log traces each step (the same JSONL trace seam the rest of the extension uses).
  log: (event: string, fields?: Record<string, unknown>) => void;
  // now is an injectable clock for the announcement timestamp (tests pin it).
  now?: () => Date;
  // waitMs delays between idle polls while waiting for the current turn to finish.
  // Injectable so a test drives it without real time.
  waitMs?: (ms: number) => Promise<void>;
}

// Handoff owns the wind-down for one worker. It is armed once: a second drain
// while a wind-down is already running is a no-op (idempotent), so a repeated
// drain frame cannot double-close or double-exit.
export class Handoff {
  private pending = false;

  constructor(private readonly deps: HandoffDeps) {}

  // isPending is true once a drain has been accepted. The extension's wake path
  // checks it: once pending, no new wake is enqueued or delivered — the worker is
  // winding down and must not pick up new work, or it would still be acting while
  // it claims to have relinquished (the two-owners hazard, in one process).
  isPending(): boolean {
    return this.pending;
  }

  // onDrain accepts a drain request and runs the cooperative wind-down. Idempotent:
  // a drain that arrives while one is already in flight is ignored. The sequence,
  // in order, is the single-owner contract:
  //   1. mark pending (stop taking new wakes),
  //   2. wait for the current turn to finish (idle),
  //   3. announce relinquished{session} on the bus (ownership released, here is the
  //      session to resume),
  //   4. drain + close the bus client (presence → offline; the release is now
  //      VISIBLE to any would-be re-spawner),
  //   5. exit the pi process (the JSONL is persisted for the resume).
  async onDrain(reason: string): Promise<void> {
    if (this.pending) {
      this.deps.log("handoff_drain_ignored", { detail: "already winding down" });
      return;
    }
    this.pending = true;
    this.deps.log("handoff_drain_accepted", { reason });

    await this.waitIdle();

    const session = this.deps.sessionId();
    this.deps.log("handoff_relinquish", { session });
    try {
      await this.deps.announce({
        $type: HANDOFF_TYPE,
        verb: VerbRelinquished,
        session: session || undefined,
        reason: reason || undefined,
        updated: (this.deps.now?.() ?? new Date()).toISOString(),
      });
    } catch (e) {
      // Best-effort: the announcement is the visible signal, but the close + exit
      // are the real release. A failed announce must not strand the worker owning
      // the session.
      this.deps.log("handoff_announce_error", { detail: (e as Error).message });
    }

    await this.deps.closeBus();
    this.deps.log("handoff_closed");
    this.deps.exit();
  }

  // announceAcquired is called by a RE-SPAWNED worker once it has resumed the
  // session, so the bus shows ownership returning. It is the mirror of the
  // relinquished announcement and closes the visible handoff loop.
  async announceAcquired(session: string, reason: string): Promise<void> {
    try {
      await this.deps.announce({
        $type: HANDOFF_TYPE,
        verb: VerbAcquired,
        session: session || undefined,
        reason: reason || undefined,
        updated: (this.deps.now?.() ?? new Date()).toISOString(),
      });
      this.deps.log("handoff_acquired", { session });
    } catch (e) {
      this.deps.log("handoff_announce_error", { detail: (e as Error).message });
    }
  }

  // waitIdle polls until the agent finishes its current turn, bounded so a wedged
  // turn cannot block the wind-down forever (fail-loud discipline: after the bound
  // we proceed to release anyway — the OS-level reap is the backstop). The bound is
  // generous because a real turn can run a tool for a while; it is here to prevent
  // a hang, not to cut a turn short.
  private async waitIdle(): Promise<void> {
    const waitMs = this.deps.waitMs ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
    const deadlineMs = 5 * 60_000;
    const stepMs = 250;
    let waited = 0;
    while (!this.deps.isIdle()) {
      if (waited >= deadlineMs) {
        this.deps.log("handoff_wait_idle_timeout", { waitedMs: waited });
        return;
      }
      await waitMs(stepMs);
      waited += stepMs;
    }
    this.deps.log("handoff_idle", { waitedMs: waited });
  }
}
