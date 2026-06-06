# m2-collaboration-loop.tape -- the M2 collaboration loop, recorded with VHS
# (https://github.com/charmbracelet/vhs). The scenario is tests/e2e/m2-acceptance.md,
# shown MULTI-PANE: each client gets its own tmux pane, so you watch deliveries land
# LIVE. The operator issues alice (held-identity) and bob (self-enroll); bob
# subscribes and watches in his panes; alice publishes a message and writes a
# shared artifact in hers, and the frames appear in bob's panes in real time. The
# bus is the sole minter of identities (ADR-0020) and derives presence from the
# live connection.
#
# Layout:
#   operator — issue + directory   |  bob — subscribe msg.topic.plan
#   alice    — publish + artifact   |  bob — watch the-plan
#
# Render:  vhs docs/demos/m2-collaboration-loop.tape    (from the repo root)
# Needs:   tmux, and the `sextant` binary built at /tmp/sxdemo/sextant
#          (go build -o /tmp/sxdemo/sextant ./cmd/sextant)
#
# The panes are driven by docs/demos/m2-multipane.sh (started hidden, below); the
# tape just attaches and records. Everything shown being typed is a real
# invocation against a live bus.

Output docs/demos/m2-collaboration-loop.gif
Output docs/demos/m2-collaboration-loop.mp4

Set Shell "bash"
Set FontSize 14
Set Width 1540
Set Height 920
Set Padding 12
Set Theme "Catppuccin Mocha"
Set Framerate 24

# ---- hidden setup: start the four-pane driver, wait for the session ----
Hide
Type "export SEX=/tmp/sxdemo/sextant S=/tmp/sxdemo/store SESH=m2demo" Enter
Type "bash docs/demos/m2-multipane.sh >/tmp/sxdemo/driver.log 2>&1 &" Enter
Type "until tmux has-session -t m2demo 2>/dev/null; do sleep 0.2; done" Enter
Type "clear" Enter
Show

# ---- attach and record while the driver sends commands to each pane ----
Type "tmux attach -t m2demo" Enter
Sleep 35s

# ---- end: detach, tear down the session and the bus ----
Hide
Ctrl+b
Type "d"
Sleep 600ms
Type "tmux kill-session -t m2demo 2>/dev/null; kill $(cat /tmp/sxdemo/store/bus.pid 2>/dev/null) 2>/dev/null" Enter
Show
Sleep 600ms
