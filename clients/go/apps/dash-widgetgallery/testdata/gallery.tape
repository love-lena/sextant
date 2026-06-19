# VHS tape for the dash widget gallery (TASK-7.1).
#
# Renders the cursor list, stream viewport, and detail pane in all three focus
# states, then walks the two-level focus model: step into a pane, move the
# cursor / scroll, step back out, move the selection, step into the next pane.
# It also toggles the light theme so both palettes show.
#
# Render with:  vhs cmd/dash-widgetgallery/testdata/gallery.tape
# (run from the worktree root; the binary is built into /tmp first — see below).
#
# The tape runs a prebuilt binary so recording is not gated on a compile. Build
# it first:  go build -o /tmp/dash-widgetgallery ./cmd/dash-widgetgallery

Output cmd/dash-widgetgallery/testdata/gallery.gif

Set Shell "bash"
Set FontSize 13
Set Width 1280
Set Height 760
Set Padding 16

Hide
Type "/tmp/dash-widgetgallery --theme dark"
Enter
Sleep 1.5s
Show

# Layout level: presence is selected (accent border). Step in.
Sleep 800ms
Enter
Sleep 600ms

# Active in presence: move the cursor down through the roles.
Down@350ms 4
Sleep 800ms

# Step out, move selection to the stream, step in, scroll.
Escape
Sleep 400ms
Right
Sleep 500ms
Enter
Sleep 500ms
Down@300ms 3
Sleep 600ms

# Step out, move selection to the detail pane, step in, scroll the wrapped text.
Escape
Sleep 400ms
Right
Sleep 500ms
Enter
Sleep 500ms
Down@300ms 4
Sleep 800ms

# Step back out and toggle to the light palette.
Escape
Sleep 400ms
Type "t"
Sleep 1.5s

# Walk the selection across all three panes in the light theme.
Right
Sleep 500ms
Right
Sleep 500ms
Right
Sleep 1s

Type "q"
Sleep 500ms
