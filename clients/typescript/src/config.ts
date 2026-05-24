/**
 * Configuration loader for @sextant/client.
 *
 * Mirrors pkg/client/config.go — same TOML schema, same defaults, same
 * validation rules. See specs/components/client-libraries.md §"Config
 * file".
 */

import { readFile } from "node:fs/promises";
import { homedir } from "node:os";
import path from "node:path";

import { parse as parseToml } from "smol-toml";

/** NATS connection details. */
export interface NATSConfig {
  /** Full NATS URL, e.g. `"nats://127.0.0.1:4222"`. Required. */
  url: string;
}

/** Operator credentials. Exactly one of `password` or `credsPath` must be set. */
export interface OperatorConfig {
  /** NATS auth username; defaults to `"operator"`. */
  user: string;
  /** Inline operator password (optional in test/dev). */
  password?: string;
  /** Path to a NATS creds file. `~/` is expanded against the user's home. */
  credsPath?: string;
}

/** Optional knobs; defaults filled by `loadConfig`. */
export interface ClientOptionsConfig {
  /** Cap on initial dial. Default 10s. */
  connectTimeoutMs: number;
  /** Default per-RPC timeout. Default 30s. */
  requestTimeoutMs: number;
  /** One of `trace`|`debug`|`info`|`warn`|`error`. Default `"info"`. */
  logLevel: LogLevel;
}

export type LogLevel = "trace" | "debug" | "info" | "warn" | "error";

const LOG_LEVELS: readonly LogLevel[] = ["trace", "debug", "info", "warn", "error"];

/** Full parsed `client.toml`. */
export interface ClientConfig {
  nats: NATSConfig;
  operator: OperatorConfig;
  client: ClientOptionsConfig;
}

/** Default config-file location: `~/.config/sextant/client.toml`. */
export function defaultConfigPath(): string {
  return path.join(homedir(), ".config", "sextant", "client.toml");
}

/** Expand a leading `~/` against the user's home directory. */
export function expandHome(p: string): string {
  if (p === "~") return homedir();
  if (p.startsWith("~/")) return path.join(homedir(), p.slice(2));
  return p;
}

/**
 * Parse a Go-style duration string ("10s", "500ms", "1m30s") into
 * milliseconds. Matches `time.ParseDuration` for the units we care
 * about (`ns`, `us`/`µs`, `ms`, `s`, `m`, `h`).
 */
export function parseDurationMs(input: string): number {
  const s = input.trim();
  if (!s) return 0;
  // Compound durations: "1m30s", "2h15m", etc. Walk number+unit pairs.
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let total = 0;
  let matched = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    matched += m[0].length;
    const value = Number(m[1]);
    const unit = m[2] as string;
    switch (unit) {
      case "ns":
        total += value / 1_000_000;
        break;
      case "us":
      case "µs":
        total += value / 1_000;
        break;
      case "ms":
        total += value;
        break;
      case "s":
        total += value * 1_000;
        break;
      case "m":
        total += value * 60_000;
        break;
      case "h":
        total += value * 3_600_000;
        break;
    }
  }
  if (matched !== s.length || matched === 0) {
    throw new Error(`client: parse duration ${JSON.stringify(input)}`);
  }
  return total;
}

interface RawConfig {
  nats?: { url?: string };
  operator?: {
    user?: string;
    password?: string;
    creds_path?: string;
  };
  client?: {
    connect_timeout?: string;
    request_timeout?: string;
    log_level?: string;
  };
}

/**
 * Validate and fill defaults on a half-built ClientConfig. The output
 * is fully-normalized and safe to pass to `connectWithConfig`.
 *
 * Mirrors `(Config).validateAndFill` in pkg/client/config.go.
 */
export function validateAndFill(input: {
  nats?: Partial<NATSConfig>;
  operator?: Partial<OperatorConfig>;
  client?: Partial<ClientOptionsConfig>;
}): ClientConfig {
  if (!input.nats?.url) {
    throw new Error("client: nats.url is required");
  }
  const operator: OperatorConfig = {
    user: input.operator?.user ?? "operator",
    password: input.operator?.password,
    credsPath: input.operator?.credsPath,
  };
  const hasPassword = !!operator.password;
  const hasCreds = !!operator.credsPath;
  if (!hasPassword && !hasCreds) {
    throw new Error(
      "client: exactly one of operator.password or operator.credsPath must be set",
    );
  }
  if (hasPassword && hasCreds) {
    throw new Error(
      "client: operator.password and operator.credsPath are mutually exclusive",
    );
  }
  if (operator.credsPath) {
    operator.credsPath = expandHome(operator.credsPath);
  }

  const client: ClientOptionsConfig = {
    connectTimeoutMs:
      input.client?.connectTimeoutMs && input.client.connectTimeoutMs > 0
        ? input.client.connectTimeoutMs
        : 10_000,
    requestTimeoutMs:
      input.client?.requestTimeoutMs && input.client.requestTimeoutMs > 0
        ? input.client.requestTimeoutMs
        : 30_000,
    logLevel: (input.client?.logLevel as LogLevel | undefined) ?? "info",
  };
  if (!LOG_LEVELS.includes(client.logLevel)) {
    throw new Error(
      `client: invalid client.logLevel ${JSON.stringify(client.logLevel)} (want ${LOG_LEVELS.join("|")})`,
    );
  }

  return {
    nats: { url: input.nats.url },
    operator,
    client,
  };
}

/**
 * Read a TOML file from `path`, parse it, fill defaults, return the
 * normalized ClientConfig. `~/` in `path` is expanded.
 *
 * Mirrors pkg/client.LoadConfig.
 */
export async function loadConfig(filePath: string): Promise<ClientConfig> {
  const expanded = expandHome(filePath);
  const raw = await readFile(expanded, "utf8");
  let parsed: RawConfig;
  try {
    parsed = parseToml(raw) as RawConfig;
  } catch (err) {
    throw new Error(
      `client: parse config ${expanded}: ${err instanceof Error ? err.message : String(err)}`,
    );
  }
  const input: Parameters<typeof validateAndFill>[0] = {
    nats: parsed.nats?.url ? { url: parsed.nats.url } : {},
    operator: {
      user: parsed.operator?.user,
      password: parsed.operator?.password || undefined,
      credsPath: parsed.operator?.creds_path || undefined,
    },
    client: {
      connectTimeoutMs: parsed.client?.connect_timeout
        ? parseDurationMs(parsed.client.connect_timeout)
        : undefined,
      requestTimeoutMs: parsed.client?.request_timeout
        ? parseDurationMs(parsed.client.request_timeout)
        : undefined,
      logLevel: parsed.client?.log_level as LogLevel | undefined,
    },
  };
  return validateAndFill(input);
}
