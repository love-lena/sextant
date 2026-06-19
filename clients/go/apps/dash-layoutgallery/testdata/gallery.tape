# VHS tape for the dash layout-engine gallery (ADR-0024 + ADR-0026).
#
# Drives the cockpit through its motions against mock surfaces (no bus), under
# the one-focused-pane model: one pane is always focused (active border + lit
# readout), keys go to it, and moving focus never touches a pane's content.
#
#   1. cockpit default: three browsers side by side, the first pane focused;
#      Ctrl+L / Ctrl+H move focus spatially, Tab cycles (and wraps)
#   2. content keys go to the focused pane: the mock's "last key" readout
#      updates on the focused pane only — and focus moves never change it
#   3. preset switch: p cycles cockpit → split (balanced grid); Ctrl+J/Ctrl+K
#      move focus between the split's rows; p returns to cockpit
#   4. pane toggle via options menu (o): hide artifacts, layout reflows to
#      2-col; re-show it, layout reflows back to 3-col
#   5. theme toggle via options menu: dark → light → dark
#
# The mocks satisfy the Surface contract alone (fixed body text + size/focus +
# last-key readout), so the layout composes them with no SDK and no bus.
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

# 1 — cockpit default: clients · topics · artifacts side by side, clients
# focused. Ctrl+L walks focus right; Ctrl+H walks it back; Tab cycles (from
# artifacts it wraps to clients).
Sleep 900ms
Ctrl+L
Sleep 600ms
Ctrl+L
Sleep 600ms
Ctrl+H
Sleep 600ms
Tab
Sleep 600ms
Tab
Sleep 800ms

# 2 — content keys go to the focused pane: Down updates the focused pane's
# "last key" readout. Moving focus away (Ctrl+L) and back (Ctrl+H) never
# changes any readout — the focus keys are never delivered; panes hold their
# place.
Down
Sleep 800ms
Ctrl+L
Sleep 700ms
Ctrl+H
Sleep 900ms
Ctrl+L
Sleep 700ms

# 3 — preset switch: p cycles cockpit → split (clients + topics on top,
# artifacts across the bottom). Ctrl+J drops focus to the bottom row; Ctrl+K
# lifts it back. Second p returns to cockpit.
Type "p"
Sleep 1.2s
Ctrl+J
Sleep 700ms
Ctrl+K
Sleep 700ms
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
