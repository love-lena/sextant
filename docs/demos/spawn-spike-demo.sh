#!/usr/bin/env bash
# M5.1 spawn-spike PoC demo (TASK-70) — RETIRED (TASK-239).
#
# This demo WAS the M5.1 spike: it built and ran the spike supervisor
# (clients/go/apps/, now removed by the ADR-0049 restructure) as a per-agent
# wake-loop — connect as a dispatcher, watch an agent's DM, re-invoke the one-shot
# harness on inbound (`--on-wake` / `--once`). That spike graduated into the M5.2
# reference dispatcher (clients/dispatcher), and the wake mechanism was reframed by
# ADR-0045 (a mobilized agent is a resumable one-shot function — `pi --rpc` workers).
# The supervisor binary this demo built no longer exists, so the demo can no longer
# run as written.
#
# Where the capability lives now:
#   - The reference dispatcher: clients/dispatcher (+ recipes/{agent.sh,pi.sh}).
#   - The proven spike findings (historical): docs/demos/spawn-spike-notes.md.
#   - The drain-and-revive / resumable-worker model: docs/adr/0045-*.md.
echo "docs/demos/spawn-spike-demo.sh is RETIRED (TASK-239): the M5.1 spike supervisor"
echo "graduated into clients/dispatcher and its wake-loop was reframed by ADR-0045."
echo "See clients/dispatcher (+ recipes/) and docs/demos/spawn-spike-notes.md."
exit 0
