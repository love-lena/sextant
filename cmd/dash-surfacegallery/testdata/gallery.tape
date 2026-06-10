# VHS tape for the dash surface gallery (ADR-0024 + ADR-0026).
#
# Tours the five surfaces the gallery hosts — the three master-detail browsers
# (clients, topics, artifacts) plus the two standalone detail surfaces (stream
# and artifact reader) — against seeded mock data, no bus. The shown surface is
# ALWAYS focused (no step-in level): Enter opens the selected row's detail IN
# THE SAME PANE (the title tracks it), Esc pops one level back to the list, and
# Esc at the list does nothing. An open detail is pane state: tab away and back
# and it is still open — panes hold their place.
#
# Surfaces visited in order:
#   1. clients browser → cursor down → Enter (open DM · bus) → Esc (pop) →
#      Esc again (no-op: the list is the top level)
#   2. topics browser → Enter (open Topic · build) → tab AWAY and all the way
#      around → the detail is STILL OPEN when topics comes back → Esc
#   3. artifacts browser → Enter (open Artifact · dash-plan) → Esc
#   4. stream (standalone, seeded) → type a line incl. q (the compose captures;
#      q types, t/q do not act) → theme toggle waits for a non-capturing pane
#   5. artifact reader (standalone, seeded) → scroll → t theme toggle → q quit
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

# 1 — clients browser (always focused: the cursor bar is lit from the start).
# Move the cursor, Enter opens the row's DM detail in the same pane ("DM · bus"
# title), Esc pops back to the list, and a second Esc is a NO-OP — the list is
# the pane's top level (ADR-0026: Esc never leaves a pane).
Sleep 900ms
Down@350ms 2
Sleep 500ms
Enter
Sleep 1.2s
Escape
Sleep 700ms
Escape
Sleep 700ms

# 2 — topics browser: Enter opens its conversation in place ("Topic · build").
# Then tab AWAY with the detail open and cycle all the way around (five
# surfaces) — when topics comes back, the conversation is STILL OPEN (panes
# hold their place). Esc pops it.
Tab
Sleep 700ms
Enter
Sleep 1.2s
Tab
Sleep 600ms
Tab
Sleep 600ms
Tab
Sleep 600ms
Tab
Sleep 600ms
Tab
Sleep 900ms
Escape
Sleep 700ms

# 3 — artifacts browser. Enter opens the artifact reader in the same pane
# ("Artifact · dash-plan" title; nil client in the gallery — the master↔detail
# motion is what this shows). Esc pops back.
Tab
Sleep 700ms
Enter
Sleep 1.2s
Escape
Sleep 700ms

# 4 — stream surface (standalone, seeded): 6 messages from role-hued authors,
# the compose already live (the surface is focused). Type a line with a q in it
# — the compose captures, so q just types (quitting from here is ctrl+c).
Tab
Sleep 700ms
Type@70ms "ship it - q types while composing"
Sleep 1.2s

# 5 — artifact reader (standalone, seeded): "The three browsers" document with
# the review comment affordance. The arrows scroll the body even while the
# comment line is capturing (arrows are control keys, not text).
Tab
Sleep 700ms
Down@300ms 3
Sleep 700ms

# Theme toggle + quit happen from a non-capturing pane (the printable host keys
# t/q type while a compose captures): tab wraps to the clients list, t flips
# the theme to light and back, q quits.
Tab
Sleep 700ms
Type "t"
Sleep 1.2s
Type "t"
Sleep 900ms
Type "q"
Sleep 500ms
