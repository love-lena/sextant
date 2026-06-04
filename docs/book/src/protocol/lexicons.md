# Lexicons

The wire envelope and the M2 record shapes, in AT-Protocol lexicon format (a
minimal subset). Ids are the NSID *name* minus its authority (deferred — see
[The protocol](../protocol.md)). Records carry `$type` from day one.

## `envelope`

The wire atom: the frozen wrapper around a typed record. Messages travel
enveloped; artifacts and registry records are stored bare.

```json
{{#include ../../../../protocol/lexicons/envelope.json}}
```

## `chat.message`

A line of dialogue on a topic or to a client. The author is the envelope
`sender`, so it is not duplicated in the record.

```json
{{#include ../../../../protocol/lexicons/chat.message.json}}
```

## `document`

An example artifact record shape — titled Markdown. Artifacts may carry any
lexicon; this is the one the getting-started walkthrough uses.

```json
{{#include ../../../../protocol/lexicons/document.json}}
```

## `client`

A clients-registry entry: the bare presence record stored under the client's id.

```json
{{#include ../../../../protocol/lexicons/client.json}}
```
