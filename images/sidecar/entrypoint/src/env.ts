/**
 * Env-var helpers extracted from `src/index.ts` so the test suite can
 * exercise them without importing index (which kicks off `main()` at
 * import time).
 *
 * Currently covers `SEXTANT_INITIAL_PROMPT` decoding — the template
 * `initial_prompt` field arrives base64-encoded so multi-line charters
 * survive the env-var transport. See
 * slug:bug-initial-prompt-not-forwarded-to-sdk.
 */

/**
 * Decode the base64-encoded `SEXTANT_INITIAL_PROMPT` env var that
 * sextantd injects from the template's `initial_prompt` field. Returns
 * `undefined` when the var is unset, empty, or fails to decode (in
 * which case the caller logs and proceeds without a system prompt
 * rather than crashing the sidecar).
 *
 * Encoding choice rationale: TOML allows multi-line / triple-quoted
 * strings and charters typically span paragraphs. Carrying that raw
 * through an env var means worrying about newline escaping at multiple
 * container-runtime layers; base64 sidesteps the whole class of bugs.
 */
export function decodeInitialPrompt(raw: string | undefined): string | undefined {
  if (!raw) return undefined;
  try {
    const decoded = Buffer.from(raw, "base64").toString("utf-8");
    if (!decoded) return undefined;
    return decoded;
  } catch {
    // Malformed base64 — let the caller log and continue without a
    // system prompt. Better than crashing the sidecar over a charter.
    return undefined;
  }
}
