#!/usr/bin/env bash
# M5.2 reference dispatcher demo (TASK-25) — RETIRED (TASK-239).
#
# This demo exercised the M5.1 spawn-spike PoC supervisor (clients/go/apps/, now
# removed by the ADR-0049 restructure) as a per-child WAKE-LOOP launched by the
# dispatcher (`--supervisor` / `--on-wake`). That wake-loop mechanism was reframed by
# ADR-0045 (a mobilized agent is a resumable one-shot function — `pi --rpc` workers),
# so the supervisor binary this demo built and ran no longer exists and the demo can
# no longer run as written.
#
# Where the capability lives now:
#   - The reference dispatcher: clients/dispatcher (subscribe to spawn.request → mint
#     a named child → launch the harness → publish spawn.ack; recursion falls out).
#     Run it via clients/dispatcher/recipes/{agent.sh,pi.sh}.
#   - The dispatch+coordinator composition is demonstrated, token-free, end-to-end by
#     docs/demos/m5-workflow-demo.sh (the coordinator dispatches agents through the
#     dispatcher and checkpoints state).
#   - Design notes (historical): docs/demos/m5-dispatcher-notes.md, docs/adr/0045-*.md.
#
# Re-pointing this script to drive the dispatcher WITHOUT the retired wake-loop is a
# possible follow-up; it is retired rather than left building a deleted path.
echo "docs/demos/m5-dispatcher-demo.sh is RETIRED (TASK-239): the M5.1 wake-loop"
echo "supervisor it ran was reframed by ADR-0045. See clients/dispatcher (+ recipes/)"
echo "and docs/demos/m5-workflow-demo.sh for the live dispatch+coordinator demo."
exit 0
