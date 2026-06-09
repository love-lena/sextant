# VHS tape for the dash (ADR-0024 + ADR-0026): the three browsers, one focused
# pane, and panes that hold their place — driven against the ACTUAL built
# `sextant dash` over a REAL embedded bus. It is the human-review surface for
# the PR — the motions the dash is for:
#
#   1. zero-config launch: `sextant dash` with no identity self-enrolls against
#      the discoverable local bus ("first run — enrolled as lena") and opens the
#      cockpit — three browsers side by side, every list populated, the first
#      pane focused
#   2. Tab focuses the topics browser; Enter on a row opens its conversation IN
#      THE SAME PANE (the pane title becomes "Topic · plan")
#   3. compose a message, Enter → it round-trips back through the bus (no
#      optimistic echo — visible means durably on the bus)
#   4. Ctrl+L moves focus to artifacts WITH THE CONVERSATION STILL OPEN — no
#      Esc unwind; the topic pane keeps holding it (THE ADR-0026 point) — and
#      Enter opens the document reader in place
#   5. Ctrl+H returns to the conversation exactly where it was; a second line
#      round-trips through the still-open compose
#   6. Esc pops one level (conversation → list); q quits from the list
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
# bus, enrolls itself, prints its one notice line, and opens the cockpit with
# the first pane focused (one pane is ALWAYS focused — no layout level).
Type "sextant dash --theme dark"
Sleep 1s
Enter
Sleep 4s

# The cockpit: clients · topics · artifacts, side by side, lists populated.
Sleep 2s

# 2 — Tab moves focus to the topics browser (the status bar names the focused
# pane); Enter opens the cursor row's conversation in the same pane.
Tab
Sleep 700ms
Enter
Sleep 2s

# 3 — compose a line and send it. It does NOT echo optimistically; it appears
# in the conversation only when the bus round-trips it back. (While the compose
# is capturing, letters like q/j/k just type — quitting is ctrl+c here.)
Type@80ms "ship it - panes hold their place"
Sleep 900ms
Enter
Sleep 2.5s

# 4 — THE POINT (ADR-0026): Ctrl+L moves focus to artifacts WITH THE
# CONVERSATION STILL OPEN. Nothing unwinds — the topic pane keeps holding its
# place (watch it keep ticking) while the reader opens in place on the right.
Ctrl+L
Sleep 1.5s
Enter
Sleep 3s

# 5 — Ctrl+H moves focus back onto the conversation, exactly where it was. The
# compose is still live: a second line round-trips through the held-open pane.
Ctrl+H
Sleep 1.2s
Type@80ms "still here - nothing unwound"
Sleep 700ms
Enter
Sleep 2.5s

# 6 — Esc pops exactly one level: conversation → the topics list. From the
# list (no compose capturing), q quits cleanly.
Escape
Sleep 1.5s
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
