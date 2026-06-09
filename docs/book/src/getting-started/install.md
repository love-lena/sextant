# Install & first connection

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.

- Prerequisites: Go toolchain; the `sextant` binary (`make install`).
- `sextant up`: what it stands up — the embedded bus, and the discovery + creds
  material a client needs to find it.
- The reserved namespace in one line: *"`sx` is Sextant's; everything else is yours."*
- Mint your first identity: `sextant clients register --self` → it lands a saved
  **context** (URL + identity + creds) so later commands need no flags.
- Verify you're connected: `sextant clients list` shows you, `online`.
- Contexts in one breath (the kubectl / `nats context` pattern) → deep-dive lives
  in the SDK section.
- Troubleshooting: the soft clock-skew warning, where creds live, the `$SEXTANT_*`
  env vars.
