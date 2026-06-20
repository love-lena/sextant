// Unit tests for the Goals PROJECTION — the co-equal peer of Go's
// project_test.go (ADR-0044). The SAME fixtures and the SAME proof-filter
// assertions, so the TS project() turns a stored goal into effective statuses
// IDENTICALLY to the Go goals.Project the dash backend used to serve. This is the
// rule a UI must NOT reimplement; the dash now calls project() directly in the
// browser, so these pin that the rule did not drift in the port.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import { project, StatusMet, StatusInProgress, type Artifact } from "../src/index.js";

// rec parses a JSON string into the JSONValue a record crosses as (the SDK's
// natural currency), matching how the dash hands project() artifact records.
function rec(s: string): JSONValue {
  return JSON.parse(s) as JSONValue;
}

test("project applies the proof-filter: an unproved met downgrades and is not counted", () => {
  const arts: Artifact[] = [
    {
      name: "goal.g1",
      revision: 7,
      record: rec(
        `{"$type":"goal","northstar":"ship it","stream":"v0.5",` +
          `"criteria":[` +
          `{"id":"c1","text":"proved done","status":"met","owner":"sirius"},` + // proved below
          `{"id":"c2","text":"claimed done, no proof","status":"met"},` + // UNPROVED met
          `{"id":"c3","text":"in flight","status":"in-progress"}],` +
          `"review":{"state":"review"}}`,
      ),
    },
    // A proof artifact backing only c1.
    { name: "the-proof", revision: 1, record: rec(`{"title":"PR","relates":[{"goal":"g1","crit":"c1","kind":"proof"}]}`) },
    // A soft related ref to c2 — does NOT prove it.
    { name: "a-note", revision: 1, record: rec(`{"title":"note","relates":[{"goal":"g1","crit":"c2","kind":"related"}]}`) },
  ];

  const views = project(arts);
  assert.equal(views.length, 1);
  const g = views[0]!;
  assert.equal(g.id, "g1");
  assert.equal(g.name, "goal.g1");
  assert.equal(g.northstar, "ship it");
  assert.equal(g.stream, "v0.5");
  assert.equal(g.revision, 7);
  assert.equal(g.review, "review");
  assert.equal(g.criteria.length, 3);

  // c1: proved met → reads met, carries the proof evidence.
  assert.equal(g.criteria[0]!.status, StatusMet);
  assert.equal(g.criteria[0]!.evidence?.length, 1);
  assert.equal(g.criteria[0]!.evidence?.[0]!.kind, "proof");
  assert.equal(g.criteria[0]!.evidence?.[0]!.name, "the-proof");

  // c2: UNPROVED met → reads in-progress (the proof-filter downgrade); the soft
  // related evidence is present but not counted as proof.
  assert.equal(g.criteria[1]!.status, StatusInProgress);
  assert.equal(g.criteria[1]!.evidence?.length, 1);
  assert.equal(g.criteria[1]!.evidence?.[0]!.kind, "related");

  // Rollup: only c1 counts as met (1 of 3), derived after the filter.
  assert.equal(g.rollup.met, 1);
  assert.equal(g.rollup.total, 3);
});

test("project ignores non-goal artifacts (they may still be proof sources)", () => {
  const arts: Artifact[] = [
    { name: "a-doc", revision: 1, record: rec(`{"title":"not a goal","body":"x"}`) },
    { name: "goal.g2", revision: 1, record: rec(`{"northstar":"y","criteria":[]}`) },
  ];
  const views = project(arts);
  assert.equal(views.length, 1);
  assert.equal(views[0]!.id, "g2");
});

test("project sorts views by artifact name and wires goal-level evidence", () => {
  const arts: Artifact[] = [
    { name: "goal.zeta", revision: 1, record: rec(`{"northstar":"z","criteria":[{"id":"c1","text":"t","status":"not-started"}]}`) },
    { name: "goal.alpha", revision: 1, record: rec(`{"northstar":"a","criteria":[{"id":"c1","text":"t","status":"not-started"}]}`) },
    // a goal-level relation (a goal, no crit) → goal-level evidence on alpha.
    { name: "design-doc", revision: 1, record: rec(`{"title":"design","relates":[{"goal":"alpha","kind":"related"}]}`) },
  ];
  const views = project(arts);
  assert.deepEqual(views.map((v) => v.name), ["goal.alpha", "goal.zeta"]);
  const alpha = views[0]!;
  assert.equal(alpha.evidence?.length, 1);
  assert.equal(alpha.evidence?.[0]!.name, "design-doc");
});
