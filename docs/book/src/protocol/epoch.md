# Epoch & versioning

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.

- What the **epoch** is: the protocol version — a single integer the bus writes
  into every frame.
- When it's checked: a **hard gate on connect**, and **re-checked per frame**
  (retained streams can outlive a protocol epoch).
- What bumps it: wire-breaking changes; the relationship to the `wire schema-compat`
  check / WireEpoch.
- What a client does on mismatch: refuse to proceed (fail loud, fail early).
- Keep it short — this is a reference note, not an essay.
