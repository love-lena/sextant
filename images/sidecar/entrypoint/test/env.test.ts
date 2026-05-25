/**
 * Unit tests for `src/env.ts::decodeInitialPrompt`.
 *
 * Regresses plans/issues/bug-initial-prompt-not-forwarded-to-sdk.md: the
 * sidecar must decode the base64-encoded SEXTANT_INITIAL_PROMPT
 * sextantd injects from the template's `initial_prompt` field, then
 * pass the decoded string to the SDK as `systemPrompt`. This test
 * pins the decoder behaviour — the index.ts wiring layers
 * `sdkOpts.systemPrompt = env.initialPrompt` on top, with no further
 * transformation, so verifying the decode contract is sufficient to
 * verify the SDK driver receives the operator's charter verbatim.
 */

import { describe, expect, it } from "vitest";
import { decodeInitialPrompt } from "../src/env.js";

describe("decodeInitialPrompt", () => {
  it("decodes a base64-encoded UTF-8 prompt", () => {
    const prompt = "You are the assistant agent. Operator is Lena Hickson.";
    const encoded = Buffer.from(prompt, "utf-8").toString("base64");
    expect(decodeInitialPrompt(encoded)).toBe(prompt);
  });

  it("preserves multi-line charters across the base64 transport", () => {
    const prompt = [
      "# Charter",
      "",
      "You are the assistant agent.",
      "Operator is Lena Hickson.",
      "Be terse.",
      "",
    ].join("\n");
    const encoded = Buffer.from(prompt, "utf-8").toString("base64");
    const decoded = decodeInitialPrompt(encoded);
    expect(decoded).toBe(prompt);
    // Double-check the bit operators most care about: newlines survive.
    expect(decoded?.split("\n").length).toBe(prompt.split("\n").length);
  });

  it("returns undefined when the env var is unset", () => {
    expect(decodeInitialPrompt(undefined)).toBeUndefined();
  });

  it("returns undefined when the env var is empty", () => {
    expect(decodeInitialPrompt("")).toBeUndefined();
  });
});
