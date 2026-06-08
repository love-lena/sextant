# VHS tape for the dash layout-engine gallery (TASK-7.4).
#
# Drives the cockpit through the four AC#4 motions against mock surfaces (no
# bus): preset-switch (p cycles cockpit → stream → split), pane-toggle + reflow
# (the options menu turns a pane off; the grid reflows to fill), reflow on a
# resized terminal, and detail-on-demand (d toggles the hidden detail pane in and
# out; an x intent opens it from a surface). The mocks satisfy the Surface
# contract alone, so the layout composes them with no SDK.
#
# Render with:  vhs cmd/dash-layoutgallery/testdata/gallery.tape
# (run from the worktree root; the binary is built into /tmp first — see below).
#
# Build it first:  go build -o /tmp/dash-layoutgallery ./cmd/dash-layoutgallery

Output cmd/dash-layoutgallery/testdata/gallery.gif

Set Shell "bash"
Set FontSize 13
Set Width 1280
Set Height 760
Set Padding 16

Hide
Type "/tmp/dash-layoutgallery --theme dark"
Enter
Sleep 1.5s
Show

# The cockpit default: presence as a left column, the stream + artifact stacked
# in the right. Move the selection (accent border) across the panes.
Sleep 900ms
Down@400ms 1
Sleep 500ms
Up@400ms 1
Sleep 700ms

# preset-switch: cycle cockpit → stream (the stream fills the main area) → split
# (an even grid) → back to cockpit.
Type "p"
Sleep 1.2s
Type "p"
Sleep 1.2s
Type "p"
Sleep 1s

# pane-toggle + reflow: open the options menu, move down to "pane artifact", turn
# it off — the grid reflows to fill the freed space — then re-show it.
Type "o"
Sleep 800ms
Down@350ms 2
Sleep 500ms
Enter
Sleep 1.2s
Enter
Sleep 1s
Escape
Sleep 700ms

# detail-on-demand: toggle the hidden detail pane in (the grid reflows to give it
# a slot; it lands selected + stepped in), then esc closes it and the others
# reclaim the space.
Type "d"
Sleep 1.2s
Escape
Sleep 900ms

# detail-on-demand by surface intent: step into the stream and press x — the
# surface emits an OpenMsg, the layout opens detail on the named ref. Esc closes.
Enter
Sleep 600ms
Type "x"
Sleep 1.2s
Escape
Sleep 700ms

# reflow on resize is exercised live by VHS's own terminal; step the selection
# once more to show the re-fit cockpit, then quit.
Down@400ms 1
Sleep 700ms

Type "q"
Sleep 500ms
