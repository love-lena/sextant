/**
 * acceptance.ts — M8 acceptance: call list_agents against a running
 * sextantd over the same NATS the Go client uses.
 *
 * This is NOT part of the CI gate (the cross-process orchestration
 * makes it noisy in vitest). To run it manually:
 *
 *   1. In one terminal:  `sextant init && sextantd`
 *   2. In another:       `cd clients/typescript && npx tsx scripts/acceptance.ts`
 *
 * The script reads `~/.config/sextant/client.toml` like the Go demo,
 * connects, calls list_agents (expects empty), and exits 0.
 */

import { connect, type ListAgentsRequest, type ListAgentsResponse } from "../src/index.js";

async function main(): Promise<void> {
  const client = await connect();
  try {
    const resp = await client.rpc<ListAgentsRequest, ListAgentsResponse>("list_agents", {});
    console.log("list_agents OK; agents:", resp.agents.length);
    if (resp.agents.length === 0) {
      console.log("(empty result — M11 will populate this once agents can be spawned)");
    } else {
      for (const a of resp.agents) {
        console.log(`  ${a.uuid} ${a.name} lifecycle=${a.lifecycle} v=${a.version}`);
      }
    }
  } finally {
    await client.close();
  }
}

main().catch((err) => {
  console.error("acceptance failed:", err);
  process.exit(1);
});
