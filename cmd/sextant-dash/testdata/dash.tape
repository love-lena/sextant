# VHS tape for the dash (ADR-0024): the three-master-detail-browsers story
# driven against the ACTUAL built `sextant dash` over a REAL embedded bus. It is
# the human-review surface for the PR — the motions the dash is for:
#
#   1. zero-config launch: `sextant dash` with no identity self-enrolls against
#      the discoverable local bus ("first run — enrolled as lena") and opens the
#      cockpit — three browsers side by side, every list populated
#   2. step into topics, Enter on a row → its conversation opens IN THE SAME
#      PANE (the pane title becomes "Topic · plan")
#   3. compose a message, Enter → it round-trips back through the bus (no
#      optimistic echo — visible means durably on the bus)
#   4. Esc pops back to the topics list (one level per Esc)
#   5. over to artifacts, Enter on a row → the document reader opens in place
#
# Render with (from the worktree root):
#   vhs cmd/sextant-dash/testdata/dash.tape
#
# setup.sh builds the binaries, brings up a hermetic bus ($SEXTANT_HOME +
# --store in a temp dir, ADR-0021), registers identities for the clients
# browser, and seeds the topics + artifact; teardown stops it all. The recording
# is self-contained.

Output cmd/sextant-dash/testdata/dash.gif

Set Shell "bash"
Set FontSize 13
Set Width 1280
Set Height 760
Set Padding 16

# Hermetic setup (hidden): build, bus up, register, seed.
Hide
Type "source cmd/sextant-dash/testdata/setup.sh"
Enter
Sleep 8s
Type "clear"
Enter
Show

# 1 — zero-config launch: no creds, no context. The dash discovers the local
# bus, enrolls itself, prints its one notice line, and opens the cockpit.
Type "sextant dash --theme dark"
Sleep 1s
Enter
Sleep 4s

# The cockpit: clients · topics · artifacts, side by side, lists populated.
Sleep 2s

# 2 — into the topics browser: select it (Right), step in (Enter), and open the
# selected topic's conversation in the same pane (Enter on "plan").
Right@400ms 1
Sleep 700ms
Enter
Sleep 700ms
Enter
Sleep 2s

# 3 — compose a line and send it. It does NOT echo optimistically; it appears in
# the conversation only when the bus round-trips it back on the subscription.
Type@80ms "ship the three browsers"
Sleep 900ms
Enter
Sleep 2.5s

# 4 — Esc pops ONE level: conversation → the topics list (same pane).
Escape
Sleep 1.5s

# 5 — over to artifacts: step out (Esc), Right, step in, Enter opens the
# document reader in place ("Artifact · the-plan", markdown via glamour).
Escape
Sleep 700ms
Right@400ms 1
Sleep 700ms
Enter
Sleep 700ms
Enter
Sleep 3s

# Pop the reader, step out, and quit cleanly.
Escape
Sleep 700ms
Escape
Sleep 700ms
Type "q"
Sleep 800ms

# Closing beat: the zero-config first run enrolled + activated a context, so the
# identity persists — the next `sextant dash` (or any command) resolves it
# silently.
Type "sextant context current"
Sleep 500ms
Enter
Sleep 2.5s

# Teardown (hidden): stop the bus + background jobs, remove the temp dir.
Hide
Type "teardown"
Enter
Sleep 1s
