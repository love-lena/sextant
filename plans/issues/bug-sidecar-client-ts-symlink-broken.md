---
title: images/sidecar/entrypoint's @sextant/client symlink is broken on main — make lint-sidecar / test-sidecar fail
status: open
priority: P3
created_at: 2026-05-26T22:25-07:00
labels: [bug, sidecar, build, devx, ci, needs-input]
discovered_in: writing the sidecar NATS reconnect fix — both `make lint-sidecar` and `make test-sidecar` fail on a clean main checkout with `Cannot find module '@sextant/client'`
---

## Needs Lena's input

The ticket body lays out three options (commit the bridge symlink, npm workspaces / tsconfig paths, file: dependency). Each has trade-offs that touch how contributors set up the repo, what CI does, and what `make bootstrap` runs. The right call depends on which tradeoffs you weight — the implementation is the easy part once the option is picked.

## Summary

`images/sidecar/entrypoint/node_modules/@sextant/client` symlinks to `../../../client-ts`, but no `client-ts` directory exists in the repo (neither at the entry path nor inside `images/sidecar/`). On a fresh checkout:

```
$ make lint-sidecar
… eslint reports `Cannot find module '@sextant/client' or its corresponding type declarations`
$ make test-sidecar
… vitest fails to load any test that imports from @sextant/client
```

The actual TypeScript client package lives at `clients/typescript/`. Building `images/sidecar/entrypoint` against it requires a bridge symlink that isn't committed (or a path-mapping override that isn't wired). The discovery during the sidecar-NATS-reconnect work was a hand-built `images/sidecar/client-ts → clients/typescript` symlink — not committed because the structural fix should be different.

## Why P3

Operator workflows (running the sidecar, running the daemon) are not affected — the sidecar container build resolves `@sextant/client` differently. The pain is squarely on contributor / agent workflows: any subagent (or human) tasked with sidecar code can't run lint or tests locally without manual symlink surgery first.

## Fix shape (likely)

Pick ONE of:

1. **Commit the bridge symlink** — `images/sidecar/client-ts → ../../clients/typescript`, then the existing `node_modules/@sextant/client → ../../../client-ts` resolves. Git supports committing symlinks; CI on a different platform should still resolve them.

2. **Use a workspace / path-mapping** — npm workspaces or a `tsconfig.json` `paths` entry to point `@sextant/client` at `clients/typescript/` directly, without traversing `node_modules/`. Removes the symlink dependence entirely.

3. **Use a file: dependency** — `"@sextant/client": "file:../../clients/typescript"` in `images/sidecar/entrypoint/package.json` and an `npm install` post-checkout step. Self-documenting but adds the install step to `make bootstrap`.

(2) is probably cleanest — workspaces are the npm-native answer and the path mapping survives a clean clone with no extra steps.

## Acceptance

- `make lint-sidecar` on a fresh clone (no manual symlinks) exits 0.
- `make test-sidecar` on a fresh clone exits 0.
- `make bootstrap` includes whatever install step is needed for (2) or (3).
- `images/sidecar/entrypoint/package.json` documents the resolution strategy in a comment if it's non-obvious.

## Related

- [[bug-sidecar-nats-disconnect-no-reconnect]] — discovered during this fix; the subagent had to bridge the symlink manually to run any tests.
- `clients/typescript/` — the actual @sextant/client source.
- `images/sidecar/entrypoint/package.json` — the consumer.
