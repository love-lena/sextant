# Overview

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.
>
> The pages that follow this one (Operations, Records & lexicons, The frame,
> Clients registry & presence) are **generated from `protocol/`** by TASK-32.2.
> This overview is the conceptual frame you write before the generated reference.

- The actors: **clients**, the **bus**, and the **backend** (opaque to clients).
- The record/frame split: **record = user space** (the client supplies it),
  **frame = bus space** (the bus stamps id · kind · epoch · author).
- The two primitives: **Messages** (in flight, on subjects/topics) vs **Artifacts**
  (at rest, named + versioned, one author at a time).
- **A record is any valid lexicon; the primitive is the container, not the lexicon.**
  An artifact is a named/versioned slot whose record can be *any* lexicon — `document`
  is just one example, not "the artifact type." Likewise a message carries any record
  lexicon (`chat.message` is one). Worth stating plainly — it's an easy conflation.
- A **call** (client↔bus) vs **request/reply** (client↔client) — name the
  distinction once, clearly.
- Identity is **bus-enforced**: `author` is taken from the authenticated request
  and can't be forged by editing the record.
- The **epoch** gate in one line (checked on connect; see Epoch & versioning).
- A short "how to read the reference that follows" map.
