# The frame

> 🚧 **Claude outline — TODO for Lena.** Write the prose; the field table below is
> generated from `protocol/lexicons/frame.json` and fills in automatically. Delete
> this banner when the page is written.

Suggested coverage:

- What a frame *is*: the bus-stamped wire wrapper around a record.
- **Record = user space, frame = bus space** — the client supplies the record; the
  bus produces the frame.
- The bus stamps `id` · `kind` · `epoch` · `author`; `author` is unforgeable (taken
  from the authenticated request, not the record).
- `kind` discriminates: a **message** in flight vs an **artifact** at rest;
  `revision`/timestamps appear only for artifacts.

## Fields

{{#include ../../generated/lexicon-frame.md}}
