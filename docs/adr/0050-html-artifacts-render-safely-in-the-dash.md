---
status: proposed
date: 2026-06-26
---

# HTML artifacts render safely (sanitized) in the dash

Agents and the operator produce rich HTML documents — reports, roadmaps,
mockups, dashboards. The dash renders artifact and brief bodies as markdown
or plaintext, so HTML content arrives as escaped source, not as intended.
This records the decision for how the dash renders HTML content safely
without introducing a new primitive or widening the protocol.

## The `format` marker is a property of the opaque record

An artifact or brief record MAY carry an optional `format` property with two
values: `"markdown"` (the default; also the meaning when the field is absent)
or `"html"`. It is a property of the **record itself**, not the frame or wire
envelope — content stays opaque to the substrate, and the marker and the
rendering path are **client concerns** exclusively. Nothing in the bus,
the protocol, or the SDK inspects or routes on `format`; only the rendering
client reads it.

## The dash selects the render path by `format`

When `format` is `"html"` (or coerces to it), the dash sanitizes the body
with the already-vendored **DOMPurify** (v3.1.6, default configuration) and
inlines the result. When `format` is `"markdown"` or absent, the existing
`marked` → DOMPurify path is unchanged.

DOMPurify strips `<script>` elements, `on*` event-handler attributes, and
`javascript:` URLs (it also drops `<iframe>`/`<object>`/`<embed>`). Rendered
HTML content therefore executes no JavaScript and cannot reach the page's bus
client, token, or credentials.
Inline `style` and `class` attributes survive sanitization, so mockups and
reports render close to intent.

**No sandboxed iframe is introduced.** A trust model built on DOMPurify
sanitization is appropriate for static documents produced by a trusted
operator and is simpler to deploy and inspect than an iframe + cross-frame
messaging layer.

## Interactive HTML is a separate, deferred decision

Sanitized rendering covers static documents. HTML artifacts that carry
JavaScript intended to run — interactive charts, embedded widgets — require
a sandboxed iframe with a constrained cross-frame messaging model. That work
is scoped to **TASK-133** (`feat-native-html-artifacts-inline-interaction`),
which this ADR explicitly does not cover. ADR-0050 is the static-rendering
half; TASK-133 is the interactive half.

## Consequences

The known tradeoff: sanitized HTML may still cause the browser to fetch
external sub-resources (e.g., `<img src>` pointing at a remote URL). This is
acceptable for static documents authored by a trusted operator and is noted
for the deferred interactive ticket, where a sandboxed iframe would contain
it further. No change to the locked core, the wire API, or the SDK — the
`format` marker and the render switch are entirely within the dash client.
