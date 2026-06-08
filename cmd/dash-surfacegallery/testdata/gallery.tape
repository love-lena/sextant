# VHS tape for the dash surface gallery (TASK-7.3).
#
# Walks the three M4 pane-surfaces against seeded mock data (no bus): presence
# with its status glyphs, the message stream with role-hued authors and a live
# compose, and the artifact document reader + review comment. Each surface mounts
# unchanged inside the shared widget.Box chrome, proving the Surface contract.
#
# Render with:  vhs cmd/dash-surfacegallery/testdata/gallery.tape
# (run from the worktree root; the binary is built into /tmp first — see below).
#
# The tape runs a prebuilt binary so recording is not gated on a compile. Build
# it first:  go build -o /tmp/dash-surfacegallery ./cmd/dash-surfacegallery

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

# presence: the clients directory. Step in and move the cursor through the roles
# (online ● vs offline ○; display names in role hue).
Sleep 900ms
Enter
Sleep 500ms
Down@350ms 4
Sleep 700ms
Escape
Sleep 500ms

# message stream: one read-stream of chat.messages, authors hued by role. Step
# in and compose a line (it would publish + round-trip on a live bus; here the
# demo shows the compose affordance).
Tab
Sleep 700ms
Enter
Sleep 500ms
Type@70ms "ship the surfaces"
Sleep 900ms
Escape
Sleep 500ms

# artifact: the document reader (markdown via glamour) with a thin review
# affordance. Step in, scroll the body, and leave a review comment.
Tab
Sleep 700ms
Enter
Sleep 500ms
Down@300ms 3
Sleep 700ms
Type@70ms "tighten the intro"
Sleep 900ms
Escape
Sleep 500ms

# Toggle to the light palette (the surfaces re-theme), then back to dark, and
# walk back across all three surfaces.
Type "t"
Sleep 1.2s
Tab
Sleep 600ms
Tab
Sleep 600ms
Type "t"
Sleep 900ms
Tab
Sleep 700ms

Type "q"
Sleep 500ms
