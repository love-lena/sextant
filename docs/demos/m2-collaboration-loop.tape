# m2-collaboration-loop.tape -- the M2 collaboration loop, recorded with VHS
# (https://github.com/charmbracelet/vhs). Two clients (alice, bob) collaborate
# THROUGH the bus: the bus mints their identities, stamps every frame, and
# serves the protocol operations. Nothing talks directly.
#
# Render:  vhs docs/demos/m2-collaboration-loop.tape    (from the repo root)
# Needs:   the `sextant` binary built at /tmp/sxdemo/sextant
#          (go build -o /tmp/sxdemo/sextant ./cmd/sextant)
#
# The hidden setup block below just defines a `sextant` shim + a $S store dir so
# the recorded commands read like real operator commands. Everything you see
# typed is a real invocation against a live bus.

Output docs/demos/m2-collaboration-loop.gif
Output docs/demos/m2-collaboration-loop.mp4

Set Shell "bash"
Set FontSize 16
Set Width 1320
Set Height 820
Set Padding 22
Set Theme "Catppuccin Mocha"
Set TypingSpeed 28ms

# ---- hidden setup: store dir + a `sextant` shim so commands read cleanly ----
Hide
Type "export S=/tmp/sxdemo/store" Enter
Type "rm -rf $S && mkdir -p $S" Enter
Type 'sextant() { /tmp/sxdemo/sextant "$@"; }' Enter
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 800ms

# ---- 1. start the bus: one process, the bus IS the protocol ----
Type "# 1. start the bus -- one process implements the protocol over a backend"
Enter
Type "sextant up --store $S --port 4400 &"
Enter
Sleep 2s

# ---- 2. mint two clients: the bus assigns each a trusted ULID ----
Type "# 2. mint two clients -- the bus assigns each its own trusted ULID identity"
Enter
Type "sextant token alice --store $S --out $S/alice.creds"
Enter
Sleep 1200ms
Type "sextant token bob   --store $S --out $S/bob.creds"
Enter
Sleep 1500ms

# ---- 3. bob subscribes (live) ----
Type "# 3. bob subscribes to a topic -- live delivery"
Enter
Type "sextant subscribe msg.topic.standup --creds $S/bob.creds --store $S &"
Enter
Sleep 1800ms

# ---- 4. alice publishes; the frame arrives on bob's subscription live ----
Type "# 4. alice publishes -- watch it land on bob's subscription, stamped by the bus"
Enter
Type `sextant publish msg.topic.standup '{"text":"shipping the call-based bus today"}' --creds $S/alice.creds --store $S`
Enter
Sleep 2800ms

# ---- 5. read the full stamped frame ----
Type "# 5. read it back as a full frame -- the bus stamped id / author / kind / epoch"
Enter
Type "sextant read msg.topic.standup --json --creds $S/alice.creds --store $S"
Enter
Sleep 3500ms

# ---- 6. the live directory ----
Type "# 6. who is connected right now (ULID + display_name)"
Enter
Type "sextant clients list --creds $S/alice.creds --store $S"
Enter
Sleep 3500ms

# ---- 7. a shared artifact with compare-and-set ----
Type "# 7. a shared artifact -- alice drafts it"
Enter
Type `sextant artifact create design-notes '{"status":"draft","owner":"alice"}' --creds $S/alice.creds --store $S`
Enter
Sleep 2200ms
Type "#    bob revises it -- compare-and-set on revision 1 (one author at a time)"
Enter
Type `sextant artifact update design-notes '{"status":"reviewed","by":"bob"}' --rev 1 --creds $S/bob.creds --store $S`
Enter
Sleep 2200ms
Type "# 8. alice sees bob's revision -- now at revision 2"
Enter
Type "sextant artifact get design-notes --creds $S/alice.creds --store $S"
Enter
Sleep 4s

# ---- end: drain the bus ----
Hide
Type "kill %1 %2 2>/dev/null" Enter
Show
Sleep 1s
