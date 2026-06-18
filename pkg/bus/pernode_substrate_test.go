package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/nats-io/nats.go"
)

// This is the slice-1 AFK ACCEPTANCE test for the per-node JetStream substrate
// (v0.6, [[task125-offline-replication-options]]). Where pernode_mirror_test.go
// confirms the load-bearing MECHANISM at the raw nats-server level, this drives
// the SUBSTRATE I built (Config.NodeID + Config.Peers, the per-node bucket naming,
// the mirror provisioning, and the read-union in the artifact handlers) end-to-end
// through the real bus.Start + the wire API, with a client on each node:
//
//   - two nodes A and B, each a bus.Start with its OWN NodeID (its own JS domain),
//     B linked to A over a leaf and mirroring A's artifacts bucket;
//   - A's client creates an artifact -> B's client reads it (the mirror union);
//   - each node also reads + writes its OWN artifacts locally;
//   - partition the leaf link -> BOTH nodes keep serving LOCAL reads (their own
//     bucket and the last-synced mirror);
//   - reconnect -> B's mirror catches up the gap A wrote during the partition.
//
// It is in-process (embedded nats-servers, leaf link, partition, all in one Go
// test) — that is what makes it AFK-testable, no real machines in the loop.
//
// Out of scope here (later slices): owner-only WRITE enforcement, the flat
// resolver + `writable` flag, the `-n` collision suffix, message merge. Slice 1
// proves per-node read/write + mirror union + partition/resync of the SUBSTRATE.

// pernodeBus is a started per-node bus plus the bits a restart needs (its store +
// leaf-listen address + the peer-link credential it minted).
type pernodeBus struct {
	bus        *Bus
	store      string
	leafURL    string // the nats-leaf:// URL peers link to (set on the listening node)
	leafListen string // the host:port the leaf listener binds (reused on restart)
}

// startNodeA starts node A: a per-node bus (its own JS domain) that LISTENS for
// peer leaf links. It returns the node and the peer-link credential file a peer
// uses to link to it.
func startNodeA(t *testing.T) (*pernodeBus, string) {
	t.Helper()
	store := t.TempDir()
	leafPort := freeTCPPort(t)
	leafListen := fmt.Sprintf("127.0.0.1:%d", leafPort)
	a, err := Start(t.Context(), Config{
		StoreDir:       store,
		NodeID:         "A",
		LeafListenAddr: leafListen,
	})
	if err != nil {
		t.Fatalf("start node A: %v", err)
	}
	t.Cleanup(a.Shutdown)
	// A mints the credential a peer links to it with (peerLinkPermissions: the
	// federation set + the JS replication surface the mirror needs).
	creds, err := a.MintPeerLinkCreds("A-peer-link")
	if err != nil {
		t.Fatalf("node A mint peer-link creds: %v", err)
	}
	credPath := filepath.Join(t.TempDir(), "A-peer-link.creds")
	if err := os.WriteFile(credPath, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return &pernodeBus{
		bus:        a,
		store:      store,
		leafURL:    fmt.Sprintf("nats-leaf://127.0.0.1:%d", leafPort),
		leafListen: leafListen,
	}, credPath
}

// startNodeB starts node B: a per-node bus (its own JS domain) that links to node
// A and mirrors A's artifacts bucket. linkCreds is A's peer-link credential.
func startNodeB(t *testing.T, store, aLeafURL, linkCreds string) *pernodeBus {
	t.Helper()
	if store == "" {
		store = t.TempDir()
	}
	b, err := Start(t.Context(), Config{
		StoreDir: store,
		NodeID:   "B",
		Peers: []PeerNode{{
			NodeID:    "A",
			Domain:    "A",
			RemoteURL: aLeafURL,
			LinkCreds: linkCreds,
		}},
	})
	if err != nil {
		t.Fatalf("start node B: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return &pernodeBus{bus: b, store: store}
}

// nodeClient connects a wire-API client to a per-node bus (a fresh minted
// per-client credential on that node) and returns the connection + the client id.
func nodeClient(t *testing.T, n *pernodeBus, name string) (*nats.Conn, string) {
	t.Helper()
	return connectClient(t, n.bus, name)
}

// createArtifact creates an artifact through the wire API on the given client.
func createArtifact(t *testing.T, nc *nats.Conn, id, name, record string) {
	t.Helper()
	resp := call(t, nc, id, wireapi.OpArtifactCreate, wireapi.ArtifactCreateInput{
		Name: name, Record: json.RawMessage(record),
	})
	if resp.Error != "" {
		t.Fatalf("artifact.create %q: %s", name, resp.Error)
	}
}

// getArtifactRecord gets an artifact's record through the wire API, or "" + the
// error string if it does not resolve. It is the read-union under test.
func getArtifactRecord(t *testing.T, nc *nats.Conn, id, name string) (string, string) {
	t.Helper()
	resp := call(t, nc, id, wireapi.OpArtifactGet, wireapi.ArtifactGetInput{Name: name})
	if resp.Error != "" {
		return "", resp.Error
	}
	var got wireapi.ArtifactGetOutput
	mustJSON(t, resp.Result, &got)
	return string(got.Record), ""
}

// waitArtifactRecord polls the wire-API get-union until name resolves to want, or
// fails. The mirror catch-up is asynchronous, so a read of a peer's artifact waits
// for replication rather than asserting on the first read.
func waitArtifactRecord(t *testing.T, nc *nats.Conn, id, name, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last, lastErr string
	for time.Now().Before(deadline) {
		last, lastErr = getArtifactRecord(t, nc, id, name)
		if lastErr == "" && last == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("artifact %q did not resolve to %q within %v (last=%q err=%q)", name, want, within, last, lastErr)
}

// listArtifactNames returns the flat artifact names a node lists through the wire
// API (the read-union of own bucket + peer mirrors).
func listArtifactNames(t *testing.T, nc *nats.Conn, id string) []string {
	t.Helper()
	resp := call(t, nc, id, wireapi.OpArtifactList, struct{}{})
	if resp.Error != "" {
		t.Fatalf("artifact.list: %s", resp.Error)
	}
	var out wireapi.ArtifactListOutput
	mustJSON(t, resp.Result, &out)
	names := make([]string, 0, len(out.Artifacts))
	for _, a := range out.Artifacts {
		names = append(names, a.Name)
	}
	return names
}

// TestPernodeSubstrateReadUnionAcrossLeaf is the slice-1 AFK acceptance test
// proper: a 2-node per-node-JS substrate (through bus.Start) where each node reads
// + writes its own artifacts locally and reads a peer's artifacts via the mirror
// union, across a leaf link, exercised end-to-end through the wire API.
func TestPernodeSubstrateReadUnionAcrossLeaf(t *testing.T) {
	nodeA, peerLinkCreds := startNodeA(t)
	nodeB := startNodeB(t, "", nodeA.leafURL, peerLinkCreds)
	if err := nodeB.bus.WaitLeafLinked(linkCtx(t)); err != nil {
		t.Fatalf("node B leaf link did not come up: %v", err)
	}

	clientA, idA := nodeClient(t, nodeA, "client-on-A")
	clientB, idB := nodeClient(t, nodeB, "client-on-B")

	// A's client creates an artifact in A's OWN bucket (ARTIFACTS_A).
	createArtifact(t, clientA, idA, "plan-from-A", `{"owner":"A"}`)
	// A reads its own write immediately (own bucket).
	if rec, errS := getArtifactRecord(t, clientA, idA, "plan-from-A"); errS != "" || rec != `{"owner":"A"}` {
		t.Fatalf("A reading its own artifact: rec=%q err=%q", rec, errS)
	}
	// B reads A's artifact through the mirror union (async catch-up).
	waitArtifactRecord(t, clientB, idB, "plan-from-A", `{"owner":"A"}`, 5*time.Second)
	t.Log("OK: B reads A's artifact through the mirror union across the leaf")

	// B's client creates its OWN artifact (ARTIFACTS_B); B reads it locally.
	createArtifact(t, clientB, idB, "note-from-B", `{"owner":"B"}`)
	if rec, errS := getArtifactRecord(t, clientB, idB, "note-from-B"); errS != "" || rec != `{"owner":"B"}` {
		t.Fatalf("B reading its own artifact: rec=%q err=%q", rec, errS)
	}

	// B's flat list unions its own bucket with A's mirror — both names present.
	names := listArtifactNames(t, clientB, idB)
	if !contains(names, "plan-from-A") || !contains(names, "note-from-B") {
		t.Fatalf("B's flat list should union own + mirror; got %v", names)
	}
	t.Log("OK: B's artifact list is the flat union of its own bucket and A's mirror")
}

// TestPernodeSubstrateCrossNodeWriteIsolation is the slice-1 WRITE-ISOLATION
// acceptance: a node that only OWNS its bucket and has NO peers must NOT see
// another node's artifacts — a write to ARTIFACTS_B must stay on node B — AND
// mirror replication (B reading A via the cross-domain $JS API) must STILL work
// alongside it, through the REAL mirror (not a federated double-serve).
//
// The leak this pins, and its real cause: the per-node peer link first carried the
// ADR-0038 wire-API federation set (sx.api.>/sx.deliver.>), inherited from
// leafLinkPermissions. That set is correct for an engine-less v0.5 leaf (its agents
// must reach the HUB's engine), but WRONG here — each per-node bus runs its OWN
// engine and subscribes sx.api.*.>, so federating the wire API made BOTH nodes serve
// each other's client calls. B's "create note-from-B" reached A, A served it, and A
// wrote it into A's OWN ARTIFACTS_A (the leak: A authored a foreign node's write).
// That double-serve also MASKED a second bug — B's KV mirror of A never actually
// replicated (it was missing the $JS.M.> mirror push-delivery grant), so B "read" A
// only because A was answering B's federated calls.
//
// THE FIX: the per-node peer-link credential is replication-ONLY (peerLinkPermissions
// no longer inherits the wire-API set; it grants exactly $JS.<srcDomain>.API.> +
// $JS.M.> + $JSC.R.>). With no wire-API grant, the engines stop double-serving (the
// leak closes), and with $JS.M.> the mirror genuinely replicates — so B reads A from
// its OWN local mirror, not A's backend. (Owner-only WRITE enforcement — a clean
// "owned by another node" on a client update — is still slice 2; this is the
// structural transport-level isolation.)
func TestPernodeSubstrateCrossNodeWriteIsolation(t *testing.T) {
	nodeA, peerLinkCreds := startNodeA(t)
	nodeB := startNodeB(t, "", nodeA.leafURL, peerLinkCreds)
	if err := nodeB.bus.WaitLeafLinked(linkCtx(t)); err != nil {
		t.Fatalf("node B leaf link did not come up: %v", err)
	}
	clientA, idA := nodeClient(t, nodeA, "client-on-A")
	clientB, idB := nodeClient(t, nodeB, "client-on-B")

	createArtifact(t, clientA, idA, "plan-from-A", `{"owner":"A"}`)
	// Mirror replication works through the REAL mirror ($JS.<A>.API source + $JS.M.>
	// delivery), not a federated double-serve: B reads A's artifact through the union.
	waitArtifactRecord(t, clientB, idB, "plan-from-A", `{"owner":"A"}`, 5*time.Second)
	createArtifact(t, clientB, idB, "note-from-B", `{"owner":"B"}`)
	// Give any (now-impossible) cross-leaf wire-API double-serve a window to NOT fire.
	time.Sleep(500 * time.Millisecond)

	// Isolation: A does NOT mirror B (A has no peers), so A must see ONLY its own —
	// B's write must not have been served+authored by A's engine.
	namesA := listArtifactNames(t, clientA, idA)
	if !contains(namesA, "plan-from-A") || contains(namesA, "note-from-B") {
		t.Fatalf("write isolation broken — A (no peers) should list only its own artifacts; got %v", namesA)
	}
	// B reads A from its OWN local mirror bucket — assert against B's local backend,
	// NOT the wire API, so a regressed double-serve (A answering B's call) cannot make
	// this pass. The record must be present locally on B.
	if rec, _, err := nodeB.bus.backend.Get(t.Context(), artifactsBucketFor("A"), "plan-from-A"); err != nil || len(rec) == 0 {
		t.Fatalf("B must read A's artifact from its OWN local mirror (real replication, not double-serve); local get err=%v rec=%q", err, string(rec))
	}
	// And B still unions both its own write and A's mirror through the wire API.
	namesB := listArtifactNames(t, clientB, idB)
	if !contains(namesB, "plan-from-A") || !contains(namesB, "note-from-B") {
		t.Fatalf("B should union its own bucket + A's mirror; got %v", namesB)
	}
}

// TestPernodeSubstratePartitionAndResync is the slice-1 AFK acceptance test for
// the offline path: with the substrate up, partition the leaf link, confirm BOTH
// nodes keep serving LOCAL reads, write to A DURING the partition, then reconnect
// and confirm B's mirror catches up the gap (partition-tolerant resync) — through
// the wire API, all in-process.
func TestPernodeSubstratePartitionAndResync(t *testing.T) {
	nodeA, peerLinkCreds := startNodeA(t)
	nodeB := startNodeB(t, "", nodeA.leafURL, peerLinkCreds)
	if err := nodeB.bus.WaitLeafLinked(linkCtx(t)); err != nil {
		t.Fatalf("node B leaf link did not come up: %v", err)
	}

	clientA, idA := nodeClient(t, nodeA, "client-on-A")
	clientB, idB := nodeClient(t, nodeB, "client-on-B")

	// Seed an artifact on A and a local one on B, and confirm B sees both.
	createArtifact(t, clientA, idA, "seed-A", `{"v":1}`)
	createArtifact(t, clientB, idB, "local-B", `{"v":1}`)
	waitArtifactRecord(t, clientB, idB, "seed-A", `{"v":1}`, 5*time.Second)

	// === Partition: drop B (the mirror holder), leaving its store. A stays up. ===
	bStore := nodeB.store
	nodeB.bus.Shutdown()
	waitLeafUnlinked(t, nodeA.bus.ns, 5*time.Second)

	// A keeps serving + accepting LOCAL writes while B is partitioned away.
	if rec, errS := getArtifactRecord(t, clientA, idA, "seed-A"); errS != "" || rec != `{"v":1}` {
		t.Fatalf("partitioned A reading its own artifact: rec=%q err=%q", rec, errS)
	}
	createArtifact(t, clientA, idA, "during-partition-A", `{"v":2}`)
	t.Log("OK: A serves + accepts local writes while B is partitioned away")

	// === Reconnect: bring B back on its SAME store; the mirror resumes + resyncs. ===
	nodeB = startNodeB(t, bStore, nodeA.leafURL, peerLinkCreds)
	if err := nodeB.bus.WaitLeafLinked(linkCtx(t)); err != nil {
		t.Fatalf("node B leaf link did not come back up: %v", err)
	}
	clientB2, idB2 := nodeClient(t, nodeB, "client-on-B-2")

	// B's own bucket survived the bounce (local durability).
	if rec, errS := getArtifactRecord(t, clientB2, idB2, "local-B"); errS != "" || rec != `{"v":1}` {
		t.Fatalf("reconnected B reading its own (durable) artifact: rec=%q err=%q", rec, errS)
	}
	// The mirror catches up the gap A wrote during the partition.
	waitArtifactRecord(t, clientB2, idB2, "during-partition-A", `{"v":2}`, 10*time.Second)
	// And the pre-partition value is still there (no loss).
	waitArtifactRecord(t, clientB2, idB2, "seed-A", `{"v":1}`, 2*time.Second)
	t.Log("OK: B's mirror resynced the partition gap after reconnect (partition-tolerant)")
}

// TestPernodeSubstrateSingleHubUnchanged pins the additivity guarantee: a bus
// started with NO NodeID and NO peers behaves exactly like the single hub — it
// owns the original global ARTIFACTS bucket and an artifact round-trips through
// the wire API as before. (The broader single-hub regression is the rest of the
// pkg/bus suite, unchanged; this is a focused statement of the invariant.)
func TestPernodeSubstrateSingleHubUnchanged(t *testing.T) {
	b := startTestBus(t)
	// No NodeID → no JetStream domain: the bus uses the UNSCOPED jetstream handle
	// (newJetStream's b.nodeID=="" branch), byte-identical to before this slice. The
	// nodeID is what Start threads into Options.JetStreamDomain, so an empty nodeID is
	// exactly "no domain set".
	if b.nodeID != "" {
		t.Fatalf("single hub must set no JetStream domain (nodeID), got %q", b.nodeID)
	}
	if got := b.ownArtifactsBucket(); got != "ARTIFACTS" {
		t.Fatalf("single hub own bucket = %q, want the global ARTIFACTS", got)
	}
	if buckets := b.artifactReadBuckets(); len(buckets) != 1 || buckets[0] != "ARTIFACTS" {
		t.Fatalf("single hub read buckets = %v, want exactly [ARTIFACTS]", buckets)
	}
	nc, id := connectClient(t, b, "single-hub-client")
	createArtifact(t, nc, id, "plain", `{"single":true}`)
	if rec, errS := getArtifactRecord(t, nc, id, "plain"); errS != "" || rec != `{"single":true}` {
		t.Fatalf("single-hub artifact round-trip: rec=%q err=%q", rec, errS)
	}
}

// linkCtx returns a context bounded for waiting on a leaf link to come up.
func linkCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// contains reports whether s contains v.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
