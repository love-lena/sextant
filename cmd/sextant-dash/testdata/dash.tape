# VHS tape for the dash (TASK-7.5, AC#3 part B): the end-to-end narrative driven
# against the ACTUAL built `sextant dash` over a REAL embedded bus. It is the
# human-review surface for the PR — the four motions the dash is for:
#
#   1. launch → see presence (the clients directory, online ●) + a live stream
#   2. observe the live message stream (seeded backlog + a background publisher)
#   3. step into the stream, type a message, Enter → it round-trips back (no
#      optimistic echo — visible means durably on the bus)
#   4. step out, then open the artifact in the detail-on-demand pane
#
# Render with (from the worktree root):
#   vhs cmd/sextant-dash/testdata/dash.tape
#
# setup.sh builds the binaries, brings up a hermetic bus ($SEXTANT_HOME + --store
# in a temp dir, ADR-0021), enrols the dash + presence identities, and seeds the
# stream + artifact; teardown stops it all. The recording is self-contained.

Output cmd/sextant-dash/testdata/dash.gif

Set Shell "bash"
Set FontSize 13
Set Width 1280
Set Height 760
Set Padding 16

# Hermetic setup (hidden): build, bus up, enrol, seed.
Hide
Type "source cmd/sextant-dash/testdata/setup.sh"
Enter
Sleep 6s
Type "clear"
Enter

# Launch the dash under the enrolled identity (the active context, run bare).
Type "sextant dash --theme dark"
Enter
Sleep 2.5s
Show

# 1 + 2 — launch lands on the cockpit: presence (left), the live stream (right
# top), the artifact reader (right bottom). Let the seeded backlog + a heartbeat
# settle so the stream is visibly live.
Sleep 2s

# Move the selection right to the stream pane (the top-right pane) and step in.
# Arrows navigate spatially now, so Right (not Down) crosses from the presence
# column to the stream.
Right@400ms 1
Sleep 700ms
Enter
Sleep 700ms

# 3 — compose a line and send it. It does NOT echo optimistically; it appears in
# the stream only when the bus round-trips it back on the subscription.
Type@80ms "ship the dash"
Sleep 900ms
Enter
Sleep 1.8s

# Step out of the stream back to the layout level.
Escape
Sleep 700ms

# 4 — open the artifact in the detail-on-demand pane (the layout's `d` toggle):
# the document reader (markdown via glamour) appears.
Type "d"
Sleep 2s

# Step out (closes detail) and quit cleanly.
Escape
Sleep 700ms
Type "q"
Sleep 800ms

# Teardown (hidden): stop the bus + background jobs, remove the temp dir.
Hide
Type "teardown"
Enter
Sleep 1s
