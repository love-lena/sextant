# Records & lexicons

> 🚧 **Claude outline — TODO for Lena.** Write the prose; the field tables below are
> generated from `protocol/lexicons/*.json` and fill in automatically. Delete this
> banner when the page is written.

Suggested coverage (page intro):

- A **record** is the typed content a client supplies; its type is a *lexicon*.
- **The big one to state plainly:** a frame's record can be **any** valid lexicon.
  The ones below are the *reference* record types Sextant defines — not the only
  ones, and not the primitives themselves. `document` is one example of a record an
  artifact can hold; `chat.message` is one example of a record a message can carry.
- The bus-space lexicons — the [frame](frame.md) and the
  [client directory entry](registry.md) — live on their own pages.

## `chat.message`

_A line of dialogue on a topic or in a direct exchange. (Write the prose.)_

{{#include ../../generated/lexicon-chat-message.md}}

## `document`

_A titled Markdown document — one record type among others. (Write the prose.)_

{{#include ../../generated/lexicon-document.md}}
