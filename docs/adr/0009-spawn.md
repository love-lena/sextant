---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Spawn

Bringing a new client into existence is a **convention**: a client (or a human,
or a bridge) publishes a **spawn-request** message, and a **Dispatcher** — a
client you run — honours it by launching a harness however it chooses (a
subprocess, `docker run`, `ssh`, a cloud API). The new client connects to the bus
as an ordinary participant and starts working.

Sextant carries the request; the Dispatcher does the launching. Because the
Dispatcher is just a client, *how* spawning happens is yours to choose and swap —
local subprocesses today, the Agent SDK tomorrow — with no change to Sextant.
Sextant ships a forkable reference Dispatcher (local subprocess) to start from.

- **"Subagents" are recursion.** A spawned client can publish its own
  spawn-requests; the Dispatcher honours them the same way, whoever asked.
- **Lineage is a correlation field** (`job` / `parent`) carried in the record, so
  tools can group "everything working on issue-42." It's derivable, not a tree
  Sextant maintains.
- **Spawn-on-demand needs a Dispatcher running.** Something has to be listening in
  order to launch things — and that something is a client you run, unprivileged
  and swappable.

Map (ADR-0003): Spawn-request (convention), Dispatcher (reference client).
