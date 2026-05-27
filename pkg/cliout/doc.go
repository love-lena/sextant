// Package cliout — schema evolution rule.
//
// Every `sextant <cmd> --json` site emits an Envelope. The envelope's
// `meta.version` pins the schema version downstream scripts can branch
// on. The rules:
//
// # Additive changes — no version bump
//
//   - Adding a new field to Envelope.Data or any payload struct (the
//     field is optional, omitempty-friendly, and JSON-decoders will
//     ignore unknown fields by default).
//   - Adding a new variant to an existing enum AT THE END of its set
//     of values (existing values stay at their existing positions).
//   - Adding a new error code constant (CodeFoo). Downstream scripts
//     branch on stable codes via .error.code; adding a code is no
//     different from adding a new lifecycle state.
//
// # Breaking changes — bump meta.version, gate on --meta-version=N
//
//   - Renaming a JSON field (e.g. `created_at` → `createdAt`).
//   - Removing a field entirely.
//   - Reordering enum values (some consumers assume ordering).
//   - Changing the type of a field (string → int, etc.).
//   - Removing an error code constant.
//
// When a breaking change is required:
//
//  1. Bump `EnvelopeVersion` (this package) to the new major.
//  2. Add a `--meta-version=N` flag to the affected command(s). The
//     default emits v1 for one release; passing the flag opts into v2.
//  3. After one minor release, flip the default to v2 and keep the
//     flag for one more release to ease rollouts.
//  4. After two releases, drop the flag and the v1 emission path.
//
// The rule is intentionally conservative: scripts that pipe `--json`
// output through jq are load-bearing for sextant's operator
// experience. A silent schema change breaks them without notice; a
// version bump signals intent.
//
// # Error codes
//
// Codes are screaming-snake-case identifiers (see envelope.go). They
// are stable: scripts can switch on them, and we treat them like part
// of the public API. Messages are human-readable and may change
// between releases — never branch on the message body.
package cliout
