# tests/visual/chat_default.tape — capture the default chat TUI render
# against the pkg/fixtures Demo fixture. Drives the design loop per
# feat-tui-vhs-fixture-design-loop.
#
# Output: screenshots/chat_default.png (and chat_default.gif if VHS
# is configured for it).
#
# Run via `make screenshots`.

Output screenshots/chat_default.png
Set Width 100
Set Height 30
Set FontSize 14
Set Theme "Tomorrow Night"

Type "go run ./cmd/sextant-tui-chat-preview"
Enter
Sleep 2s
Screenshot screenshots/chat_default.png
