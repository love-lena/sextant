# VHS tape for the dash layout-engine gallery (ADR-0024, R3).
#
# Drives the cockpit through its current motions against mock surfaces (no bus):
#   1. cockpit default: three browsers side by side; spatial selection (← →)
#   2. step into a pane (Enter → active); step out (Esc → selected)
#   3. preset switch: p cycles cockpit → split (balanced grid) → cockpit
#   4. pane toggle via options menu (o): hide artifacts, layout reflows to 2-col;
#      re-show it, layout reflows back to 3-col
#   5. theme toggle via options menu: dark → light → dark
#
# The mocks satisfy the Surface contract alone (fixed body text + size/focus
# readout), so the layout composes them with no SDK and no bus.
#
# Render with (from the worktree root):
#   vhs cmd/dash-layoutgallery/testdata/gallery.tape
# Build first:  go build -o /tmp/dash-layoutgallery ./cmd/dash-layoutgallery

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

# 1 — cockpit default: clients · topics · artifacts side by side, equal columns.
# Move the selection (accent border) right across the panes.
Sleep 900ms
Right@400ms 1
Sleep 500ms
Right@400ms 1
Sleep 700ms
Left@400ms 1
Sleep 500ms

# 2 — step into the selected pane (Enter → active; hint bar switches to
# "esc back"). The mock shows "esc to step out" in its body. Esc → back to layout.
Enter
Sleep 1s
Escape
Sleep 700ms

# 3 — preset switch: p cycles cockpit → split → cockpit. In split the panes
# reflow into a balanced grid (top row: clients + topics; bottom row: artifacts
# full-width). Second p returns to cockpit.
Type "p"
Sleep 1.2s
Type "p"
Sleep 1.2s

# 4 — pane toggle via options menu (o): cursor starts on "pane clients"; Down
# Down lands on "pane artifacts [on]"; Enter hides it — the layout reflows to two
# equal columns. The menu stays open; Enter again re-shows it — back to 3-col.
# Esc closes the menu.
Type "o"
Sleep 800ms
Down@350ms 2
Sleep 500ms
Enter
Sleep 1s
Enter
Sleep 1s
Escape
Sleep 700ms

# 5 — theme toggle via options menu: Down four times from the top reaches
# "theme: dark ↔"; Enter switches to light; Enter again switches back to dark.
# Esc closes.
Type "o"
Sleep 600ms
Down@300ms 4
Sleep 400ms
Enter
Sleep 1s
Enter
Sleep 1s
Escape
Sleep 700ms

Type "q"
Sleep 500ms
