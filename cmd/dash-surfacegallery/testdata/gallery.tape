# VHS tape for the dash surface gallery (ADR-0024, R3).
#
# Tours the five surfaces the gallery hosts — the three master-detail browsers
# (clients, topics, artifacts) plus the two standalone detail surfaces (stream
# and artifact reader) — against seeded mock data, no bus. Each browser
# demonstrates the signature ADR-0024 interaction: Enter steps into the surface,
# a second Enter opens the selected row's detail IN THE SAME PANE (title tracks
# the open detail), and Esc pops back to the list.
#
# Surfaces visited in order:
#   1. clients browser → Enter (step in) → Enter (open DM · bus) → Esc (pop to list) → Esc (deselect)
#   2. topics browser → Enter → Enter (open Topic · build) → Esc → Esc
#   3. artifacts browser → Enter → Enter (open Artifact · dash-plan) → Esc → Esc
#   4. stream (standalone, seeded messages) → Enter → type a line → Esc
#   5. artifact reader (standalone, seeded document) → Enter → scroll → Esc
#   theme toggle (light ↔ dark) between steps
#
# Render with (from the worktree root):
#   vhs cmd/dash-surfacegallery/testdata/gallery.tape
# Build first:  go build -o /tmp/dash-surfacegallery ./cmd/dash-surfacegallery

Output cmd/dash-surfacegallery/testdata/gallery.gif

Set Shell "bash"
Set FontSize 13
Set Width 1280
Set Height 760
Set Padding 16

Hide
Type "/tmp/dash-surfacegallery --theme dark"
Enter
Sleep 1.5s
Show

# 1 — clients browser. The list shows every registered identity with online (●)
# / offline (○) glyphs. Enter steps in; a second Enter opens the first row's DM
# detail in the same pane ("DM · bus" title); Esc pops back to the list.
Sleep 900ms
Enter
Sleep 600ms
Down@350ms 2
Sleep 500ms
Enter
Sleep 1.2s
Escape
Sleep 700ms
Escape
Sleep 500ms

# 2 — topics browser. Enter steps in; Enter on the focused row opens its
# conversation detail in the same pane ("Topic · build" title); Esc pops back.
Tab
Sleep 700ms
Enter
Sleep 600ms
Enter
Sleep 1.2s
Escape
Sleep 700ms
Escape
Sleep 500ms

# 3 — artifacts browser. Enter steps in; Enter on the focused row opens the
# artifact reader in the same pane ("Artifact · dash-plan" title, "(nothing
# selected)" — nil client in the gallery, the master↔detail motion is what this
# shows). Esc pops back.
Tab
Sleep 700ms
Enter
Sleep 600ms
Enter
Sleep 1.2s
Escape
Sleep 700ms
Escape
Sleep 500ms

# Theme toggle to light: the browser chrome and status bar re-theme.
Type "t"
Sleep 1.2s

# 4 — stream surface (standalone, seeded). This surface starts with 6 seeded
# messages from three role-hued authors. Enter steps in and activates the compose
# field; type a line (it would publish on a live bus — here the compose affordance
# is shown). Esc steps out.
Tab
Sleep 700ms
Enter
Sleep 600ms
Type@70ms "ship the three browsers"
Sleep 900ms
Escape
Sleep 500ms

# 5 — artifact reader (standalone, seeded). Shows "The three browsers" document
# rendered with the review comment affordance. Enter steps in; scroll the body;
# Esc steps out.
Tab
Sleep 700ms
Enter
Sleep 600ms
Down@300ms 3
Sleep 700ms
Escape
Sleep 500ms

# Toggle back to dark, then quit.
Type "t"
Sleep 900ms
Type "q"
Sleep 500ms
