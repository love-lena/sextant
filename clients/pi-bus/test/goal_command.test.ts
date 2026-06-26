// Unit tests for /set-goal argument parsing. The command's goal MECHANICS live
// in @sextant/conv-goals (tested there); this only pins the thin arg parse — how
// the optional leading goalId is disambiguated from the criterion id, and that an
// invalid status is rejected with a helpful message.

import { test } from "node:test";
import assert from "node:assert/strict";
import { parseArgs } from "../src/goal_command.js";

test("explicit goalId: /set-goal <goalId> <crit> <status> [headline]", () => {
  const p = parseArgs("v0-6-0 c1 met lexicons merged", "");
  assert.deepEqual(p, { goalId: "v0-6-0", criterionId: "c1", status: "met", headline: "lexicons merged" });
});

test("default goal: /set-goal <crit> <status> [headline] uses the configured goal", () => {
  const p = parseArgs("c2 in-progress building it", "default-goal");
  assert.deepEqual(p, { goalId: "default-goal", criterionId: "c2", status: "in-progress", headline: "building it" });
});

test("default goal with no headline synthesizes one", () => {
  const p = parseArgs("c2 blocked", "g");
  assert.deepEqual(p, { goalId: "g", criterionId: "c2", status: "blocked", headline: "criterion c2 → blocked" });
});

test("no default goal and no explicit goalId → a clear error", () => {
  const p = parseArgs("c2 met", "");
  assert.ok("error" in p && /no default goal/.test(p.error));
});

test("invalid status → a clear error listing the valid ones", () => {
  const p = parseArgs("v0 c1 done-ish", "");
  assert.ok("error" in p && /status must be one of/.test(p.error));
});

test("too few tokens → usage", () => {
  const p = parseArgs("c1", "g");
  assert.ok("error" in p && /usage/.test(p.error));
});
