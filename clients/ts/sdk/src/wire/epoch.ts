// The protocol-epoch gate and the clock-skew check. Mirrors protocol/wire (the
// Epoch constant, CheckEpoch, CheckSkew) — the SDK writes and checks EPOCH, and
// a mismatch fails loud (ADR-0010). Additive protocol changes never bump it.

import { parseULIDMillis } from "./frame.js";

// EPOCH is the protocol epoch this SDK speaks. It bumps only on a breaking wire
// change; a co-equal client is co-equal FOR an epoch (ADR-0041). The SDK
// exact-matches it against the bus's bus_epoch at connect and against every
// frame's epoch — a mismatch is fatal.
export const EPOCH = 1;

// DEFAULT_SKEW_TOLERANCE_MS is the default maximum allowed difference between a
// ULID's embedded timestamp and the bus-stamped time (5 minutes), matching
// wire.SkewTolerance.
export const DEFAULT_SKEW_TOLERANCE_MS = 5 * 60 * 1000;

// EpochError reports a protocol-epoch mismatch — its own class so callers can
// distinguish a fatal version skew from any other failure.
export class EpochError extends Error {
  readonly got: number;
  readonly want: number;
  constructor(got: number, want: number) {
    super(`wire: protocol epoch mismatch: got ${got}, want ${want}`);
    this.name = "EpochError";
    this.got = got;
    this.want = want;
  }
}

// checkEpoch verifies an epoch exactly matches the expected one. A mismatch
// throws (ADR-0010: fail loud). Checked per-frame as well as at connect, because
// durable streams outlive epochs.
export function checkEpoch(got: number, want: number): void {
  if (got !== want) {
    throw new EpochError(got, want);
  }
}

// SkewError reports that a ULID's embedded timestamp is implausibly far from the
// bus-stamped time. The SDK quarantines the offending frame.
export class SkewError extends Error {
  constructor(
    readonly id: string,
    readonly ulidMs: number,
    readonly busMs: number,
    readonly skewMs: number,
    readonly toleranceMs: number,
  ) {
    super(
      `wire: ULID ${id} clock skew ${skewMs}ms exceeds tolerance ${toleranceMs}ms ` +
        `(ulid=${new Date(ulidMs).toISOString()} bus=${new Date(busMs).toISOString()})`,
    );
    this.name = "SkewError";
  }
}

// checkSkew throws a SkewError when the millisecond timestamp embedded in the
// ULID id is more than toleranceMs from busTime (the JetStream-stamped clock,
// not the client clock). Mirrors wire.CheckSkew. A malformed ULID throws via
// parseULIDMillis.
export function checkSkew(id: string, busTime: Date, toleranceMs: number): void {
  const ulidMs = parseULIDMillis(id);
  const busMs = busTime.getTime();
  const skew = Math.abs(ulidMs - busMs);
  if (skew > toleranceMs) {
    throw new SkewError(id, ulidMs, busMs, skew, toleranceMs);
  }
}
