# m2-collaboration-loop.tape -- the M2 collaboration loop, recorded with VHS
# (https://github.com/charmbracelet/vhs). The scenario is tests/e2e/m2-acceptance.md:
# two clients (alice, bob) collaborate THROUGH the bus. The bus is the sole minter
# of identities (ADR-0020), stamps every frame, derives presence from the live
# connection, and is the only thing that holds the signing keys.
#
# Render:  vhs docs/demos/m2-collaboration-loop.tape    (from the repo root)
# Needs:   the `sextant` binary built at /tmp/sxdemo/sextant
#          (go build -o /tmp/sxdemo/sextant ./cmd/sextant)
#
# The hidden setup block defines a `sextant` shim + a $S store dir so the recorded
# commands read like real operator commands. Everything you see typed is a real
# invocation against a live bus.

Output docs/demos/m2-collaboration-loop.gif
Output docs/demos/m2-collaboration-loop.mp4

Set Shell "bash"
Set FontSize 16
Set Width 1320
Set Height 940
Set Padding 22
Set Theme "Catppuccin Mocha"
Set TypingSpeed 26ms

# ---- hidden setup: store dir + a `sextant` shim so commands read cleanly ----
Hide
Type "export S=/tmp/sxdemo/store" Enter
Type "rm -rf $S && mkdir -p $S" Enter
Type 'sextant() { /tmp/sxdemo/sextant "$@"; }' Enter
Type "PS1='$ '" Enter
Type "clear" Enter
Show
Sleep 800ms

# ---- 1. start the bus: one process IS the protocol ----
Type "# 1. start the bus -- the bus mints identities; the signing keys never leave it"
Enter
Type "sextant up --store $S --port 4400 &"
Enter
Hide
Type "BUS=$!" Enter
Show
Sleep 2s

# ---- 2. issuance, two auth modes: operator mints alice; bob self-enrolls ----
Type "# 2. the operator mints alice (held-identity mode)"
Enter
Type "sextant clients register alice --kind worker --store $S"
Enter
Sleep 2s
Type "#    bob self-enrolls on the same box (bootstrap / locality mode)"
Enter
Type "sextant clients register --self --kind reviewer --name bob --store $S"
Enter
Sleep 2500ms

# ---- 3. bob subscribes (live) ----
Type "# 3. bob subscribes to a topic -- live delivery"
Enter
Type "sextant subscribe msg.topic.plan --creds $S/bob.creds --store $S &"
Enter
Hide
Type "BOBSUB=$!" Enter
Show
Sleep 1800ms

# ---- 4. alice publishes; the frame arrives stamped with an UNFORGEABLE author ----
Type "# 4. alice publishes -- the frame lands on bob's subscription, authored by"
Enter
Type "#    alice's bus-minted id. alice cannot stamp it with anyone else's."
Enter
Type `sextant publish msg.topic.plan '{"hello":"world"}' --creds $S/alice.creds --store $S`
Enter
Sleep 3s

# ---- 5. a shared artifact with compare-and-set ----
Type "# 5. a shared artifact -- alice drafts it"
Enter
Type `sextant artifact create the-plan '{"title":"v1"}' --creds $S/alice.creds --store $S`
Enter
Sleep 1800ms
Type "#    bob revises at the revision he last saw -- compare-and-set"
Enter
Type `sextant artifact update the-plan '{"title":"v2"}' --rev 1 --creds $S/bob.creds --store $S`
Enter
Sleep 1800ms
Type "#    a stale write (still at rev 1) is rejected"
Enter
Type `sextant artifact update the-plan '{"title":"v3"}' --rev 1 --creds $S/alice.creds --store $S`
Enter
Sleep 2800ms

# ---- 6. the live directory shows presence (derived from the connection) ----
Type "# 6. who is here right now -- presence is the connection, not a heartbeat"
Enter
Type "sextant clients list --creds $S/alice.creds --store $S"
Enter
Sleep 3s

# ---- 7. durable identity: bob disconnects, then reconnects under the SAME id ----
Type "# 7. bob disconnects -- his identity persists (offline), it is not reaped"
Enter
Type "kill $BOBSUB"
Enter
Sleep 1800ms
Type "sextant clients list --creds $S/alice.creds --store $S"
Enter
Sleep 3s
Type "#    bob reconnects with the SAME creds -- same id, flipped back online"
Enter
Type "sextant subscribe msg.topic.plan --creds $S/bob.creds --store $S &"
Enter
Hide
Type "BOBSUB=$!" Enter
Type "BOB=$(sextant clients list --creds $S/alice.creds --store $S 2>/dev/null | awk '/ bob /{print $1}')" Enter
Show
Sleep 1800ms
Type "sextant clients list --creds $S/alice.creds --store $S"
Enter
Sleep 3s

# ---- 8. retire decommissions the identity for good ----
Type "# 8. the operator retires bob -- gone from the directory for good"
Enter
Type "sextant clients retire $BOB --store $S"
Enter
Sleep 1800ms
Type "sextant clients list --creds $S/alice.creds --store $S"
Enter
Sleep 4s

# ---- end ----
Hide
Type "kill $BUS $BOBSUB 2>/dev/null" Enter
Show
Sleep 1s
