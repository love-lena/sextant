# Clients registry & presence

> 🚧 **Claude outline — TODO for Lena.** Write the prose; the field table below is
> generated from `protocol/lexicons/client.json` and fills in automatically. Delete
> this banner when the page is written.

Suggested coverage:

- The **clients registry**: the durable, bus-maintained directory of issued
  identities — written at issuance, removed only by retire.
- **Listed = issued and not retired** — so the directory includes offline clients.
- **Presence** is the read-time liveness *view* over the registry (derived from the
  live connection table), not a stored field. Name the registry-vs-presence
  distinction once, clearly.
- The id is the bus-minted ULID and the registry key; `display_name` is a non-unique
  human label.

## The directory entry

{{#include ../../generated/lexicon-client.md}}
