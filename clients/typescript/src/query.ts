/**
 * query() wrapper — calls the `query_history` RPC. Mirrors
 * pkg/client/query.go.
 */

import type { Client } from "./client.js";
import { rpc } from "./rpc.js";
import type {
  Envelope,
  QueryHistoryRequest,
  QueryHistoryResponse,
} from "./types.generated.js";

/**
 * Filter for `Client.query`. Maps onto the server-side
 * QueryHistoryRequest per
 * specs/protocols/rpc-catalog.md §"Verb payloads — M7 initial set".
 */
export interface QueryFilter {
  /** Optional exact-match subject filter. Wildcards NOT supported in M7. */
  subject?: string;
  /**
   * Filter by envelope kind. Empty means any kind. Only the first
   * element is sent on the wire — multi-kind filtering lands when a
   * real consumer needs it. Mirrors the Go QueryFilter shape.
   */
  kinds?: string[];
  /** Inclusive lower time bound (UTC). Undefined means unbounded. */
  from?: Date;
  /** Inclusive upper time bound (UTC). Undefined means unbounded. */
  to?: Date;
  /** Max rows; 0/undefined means server default. */
  limit?: number;
}

/**
 * Read past envelopes from ClickHouse via the `query_history` RPC.
 * Always returns an array — empty when no events match.
 */
export async function query(client: Client, filter: QueryFilter): Promise<Envelope[]> {
  client.ensureOpen();
  const req: QueryHistoryRequest = {
    filter: {
      subject: filter.subject ?? "",
    },
    time_range: {
      since: filter.from ? filter.from.toISOString() : "",
      until: filter.to ? filter.to.toISOString() : "",
    },
    limit: filter.limit ?? 0,
  };
  if (filter.kinds && filter.kinds.length > 0) {
    req.filter.kind = filter.kinds[0];
  }
  const resp = await rpc<QueryHistoryRequest, QueryHistoryResponse>(
    client,
    "query_history",
    req,
  );
  return resp.events ?? [];
}
