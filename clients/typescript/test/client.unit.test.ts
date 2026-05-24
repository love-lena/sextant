/**
 * Unit tests for Client static helpers that don't require a live NATS.
 * Keeps the spec-pinned reconnect knobs from drifting silently.
 */

import { describe, it, expect } from "vitest";
import { Client } from "../src/client.js";

describe("Client.buildNATSOptions", () => {
  it("pins the reconnect jitter to the 100/500 spec bounds", () => {
    const opts = Client.buildNATSOptions({
      nats: { url: "nats://127.0.0.1:4222" },
      operator: { user: "operator", password: "secret" },
      client: { connectTimeoutMs: 5000, requestTimeoutMs: 10000, logLevel: "info" },
    });
    // Spec: specs/components/client-libraries.md §"Shared concerns".
    // Mirrors pkg/client/client.go's nats.ReconnectJitter(100ms, 500ms).
    expect(opts.maxReconnectAttempts).toBe(-1);
    expect(opts.reconnectTimeWait).toBe(500);
    expect(opts.reconnectJitter).toBe(100);
    expect(opts.reconnectJitterTLS).toBe(500);
  });

  it("uses operator user/password when provided", () => {
    const opts = Client.buildNATSOptions({
      nats: { url: "nats://x:1" },
      operator: { user: "operator", password: "p" },
      client: { connectTimeoutMs: 1, requestTimeoutMs: 1, logLevel: "info" },
    });
    expect(opts.user).toBe("operator");
    expect(opts.pass).toBe("p");
  });

  it("rejects credsPath until M5 wires creds-file mode in the TS client", () => {
    expect(() =>
      Client.buildNATSOptions({
        nats: { url: "nats://x:1" },
        operator: { user: "operator", credsPath: "/tmp/operator.creds" },
        client: { connectTimeoutMs: 1, requestTimeoutMs: 1, logLevel: "info" },
      }),
    ).toThrow(/credsPath is not yet supported/);
  });

  it("lets caller-supplied natsOptions override base options", () => {
    const opts = Client.buildNATSOptions(
      {
        nats: { url: "nats://x:1" },
        operator: { user: "operator", password: "p" },
        client: { connectTimeoutMs: 1, requestTimeoutMs: 1, logLevel: "info" },
      },
      { natsOptions: { name: "my-test", reconnectJitter: 200 } },
    );
    expect(opts.name).toBe("my-test");
    // Override beat the default.
    expect(opts.reconnectJitter).toBe(200);
    // Non-overridden defaults still pinned.
    expect(opts.reconnectJitterTLS).toBe(500);
  });
});
