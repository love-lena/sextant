/**
 * Unit tests for the sidecar's connect-or-exit + reconnect-rearm
 * helpers extracted from `src/index.ts`.
 *
 * Regresses slug:bug-sidecar-nats-disconnect-no-reconnect
 * acceptance line: `TestSidecarExitsOnUnrecoverableNATSFailure` —
 * sidecar configured with a bad URL exits non-zero rather than
 * hanging. The integration-shaped repro for that lives at the daemon
 * test layer; this is the unit-level pin that protects the
 * sidecar-side wiring from regression.
 */

import { describe, expect, it } from "vitest";
import {
  connectOrExit,
  type ConnectFn,
  type ExitFn,
  type LogFn,
} from "../src/sidecar-connect.js";

interface CapturedLog {
  level: "info" | "warn" | "error";
  msg: string;
  extra?: Record<string, unknown>;
}

function newLogger(): { log: LogFn; logs: CapturedLog[] } {
  const logs: CapturedLog[] = [];
  return {
    log: (level, msg, extra) => logs.push({ level, msg, extra }),
    logs,
  };
}

describe("connectOrExit", () => {
  it("returns the connected client on success", async () => {
    const fakeClient = { tag: "client" } as unknown;
    const connect: ConnectFn = async () => fakeClient;
    const { log } = newLogger();
    const exit: ExitFn = () => {
      throw new Error("exit should not be called on success");
    };

    const result = await connectOrExit(connect, log, exit, {
      natsUrl: "nats://127.0.0.1:4222",
    });
    expect(result).toBe(fakeClient);
  });

  it("logs at error and calls exit(1) when the initial connect rejects", async () => {
    const connect: ConnectFn = async () => {
      throw new Error("connect ECONNREFUSED 127.0.0.1:4222");
    };
    const { log, logs } = newLogger();
    let exitCode: number | undefined;
    const exit: ExitFn = (code) => {
      exitCode = code;
      // Real process.exit() never returns; simulate with throw so the
      // caller path matches production (no further await runs).
      throw new Error(`__test_exit:${code}`);
    };

    await expect(
      connectOrExit(connect, log, exit, { natsUrl: "nats://bad:1" }),
    ).rejects.toThrow(/__test_exit:1/);
    expect(exitCode).toBe(1);

    // Ticket §"Fix shape" item 3 — permanent failure should surface in
    // the journal at error level so the operator (and the daemon's
    // supervisor) can correlate the exit with the cause.
    const errLog = logs.find((l) => l.level === "error");
    expect(errLog).toBeDefined();
    expect(errLog!.msg).toMatch(/nats connect failed/);
    // The original URL is part of the diagnostic surface — operators
    // skim for it in `docker logs`.
    expect(errLog!.extra?.natsUrl).toBe("nats://bad:1");
  });
});
