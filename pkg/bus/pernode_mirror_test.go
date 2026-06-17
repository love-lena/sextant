package bus

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// This file is the slice-1 DE-RISK: an in-repo, committed re-confirmation of the
// load-bearing mechanism behind the v0.6 per-node-JS substrate
// ([[task125-offline-replication-options]] / [[task125-kv-replication-spike]]).
// The throwaway /tmp spike validated the mechanism on a bare nats-server; this
// test re-runs it inside the ACTUAL sextant repo against sextant's real signing
// identity + auth model (one operator, one SEXTANT account, JWT auth) and the
// vendored nats-server v2.14.2 — so the substrate is not built on an
// unconfirmed foundation.
//
// The mechanism: TWO nodes, EACH running its OWN JetStream domain, linked over a
// leaf, in ONE Go test process. Node B holds a same-name External KV mirror of
// node A's ARTIFACTS bucket, readable via the NORMAL KV API. The four properties
// the substrate rests on, proven here:
//  1. A writes -> B reads it through the mirror (no rename; ~sub-second catch-up);
//  2. partition the leaf link -> BOTH nodes keep serving LOCAL reads;
//  3. reconnect -> the mirror catches up the gap (partition-tolerant resync);
//  4. flat-union: B reads its OWN bucket together with A's mirror as one set.
//
// Slice 1 is the SUBSTRATE ONLY. Owner-only write enforcement (the per-client
// Pub.Deny that makes the write-through mirror read-only) is slice 2 — NOT here.
// This file proves the data-movement + local-read + resync mechanism; it does not
// assert read-only.

// pernodeArtifactsBucket is the KV bucket each node owns its artifacts in for the
// de-risk test. The substrate build (step 2) keys the real per-node bucket off
// the node id; here a fixed name per node is enough to prove the mechanism.
const pernodeArtifactsBucket = "ARTIFACTS"

// pernodeNode is one node in the de-risk harness: an embedded nats-server running
// its OWN JetStream domain, plus an in-process JetStream handle. leafPort and
// storeDir are recorded so a node can be bounced on the SAME store + leaf port
// (the server's own opts are unexported).
type pernodeNode struct {
	ns            *natsserver.Server
	nc            *nats.Conn
	js            jetstream.JetStream
	domain        string
	leafPort      int
	storeDir      string
	linkCredsPath string // set on a leaf node: its leaf-link credential file (reused on restart)
}

func (n *pernodeNode) shutdown() {
	if n.nc != nil {
		n.nc.Close()
	}
	if n.ns != nil {
		n.ns.Shutdown()
		n.ns.WaitForShutdown()
	}
}

// startPernodeHub starts node A: a JetStream-domain hub that also listens for a
// leaf link, under sextant's real identity/auth. It returns the node and the
// nats-leaf:// URL the leaf links to. domain is its JetStream domain name.
func startPernodeHub(t *testing.T, id *identity, domain string) (*pernodeNode, string) {
	t.Helper()
	leafPort := freeTCPPort(t)
	storeDir := t.TempDir()
	opts := &natsserver.Options{
		ServerName:      "pernode-A",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: domain, // node A owns JS domain <domain>
		StoreDir:        storeDir,
		NoSigs:          true,
		DontListen:      true,
	}
	if err := id.serverAuthOptions(opts); err != nil {
		t.Fatalf("hub auth options: %v", err)
	}
	opts.LeafNode.Host = "127.0.0.1"
	opts.LeafNode.Port = leafPort
	n := startPernodeServer(t, opts, id, "pernode-A")
	n.leafPort = leafPort
	n.storeDir = storeDir
	return n, fmt.Sprintf("nats-leaf://127.0.0.1:%d", leafPort)
}

// startPernodeLeaf starts node B: a JetStream-domain server (NOT JS-off — slice 1
// makes a node run its OWN domain) that links to node A over a leaf, under the
// SAME operator/account trust as A (a single SEXTANT account federates within the
// one account; the spike's config). domain is B's own JS domain. It returns the
// node; the caller provisions the mirror.
func startPernodeLeaf(t *testing.T, id *identity, domain, hubLeafURL string) *pernodeNode {
	t.Helper()
	// The leaf link authenticates to the hub as a SEXTANT user (the real bus mints a
	// "sextant-leaf-link" credential for exactly this). Mint one from the shared
	// identity and point RemoteLeafOpts.Credentials at it — without a credential the
	// hub (JWT auth) never authenticates the inbound leaf and the link never forms.
	linkCreds := mintLinkCredsFile(t, id)
	n := startPernodeLeafOn(t, id, domain, hubLeafURL, t.TempDir(), linkCreds)
	return n
}

// restartPernodeLeaf re-boots node B on its SAME store + leaf-link credential so
// its mirror-stream state persists across the bounce — modelling a node that went
// offline and came back, which is exactly the resync path slice 1 must support.
func restartPernodeLeaf(t *testing.T, id *identity, domain, hubLeafURL, storeDir, linkCreds string) *pernodeNode {
	t.Helper()
	return startPernodeLeafOn(t, id, domain, hubLeafURL, storeDir, linkCreds)
}

// startPernodeLeafOn boots node B on a given store + leaf-link credential. A leaf
// node runs its OWN JetStream domain (NOT JS-off — that is the slice-1 change) and
// links to node A over a leaf, under the SAME operator/account trust as A (a
// single SEXTANT account federates within the one account; the spike's config).
func startPernodeLeafOn(t *testing.T, id *identity, domain, hubLeafURL, storeDir, linkCreds string) *pernodeNode {
	t.Helper()
	u, err := url.Parse(hubLeafURL)
	if err != nil {
		t.Fatalf("parse hub leaf url: %v", err)
	}
	opts := &natsserver.Options{
		ServerName:      "pernode-B",
		Host:            "127.0.0.1",
		Port:            -1,
		JetStream:       true,
		JetStreamDomain: domain, // node B owns its OWN distinct JS domain
		StoreDir:        storeDir,
		NoSigs:          true,
		DontListen:      true,
	}
	if err := id.serverAuthOptions(opts); err != nil {
		t.Fatalf("leaf auth options: %v", err)
	}
	opts.LeafNode.Remotes = []*natsserver.RemoteLeafOpts{{
		URLs:         []*url.URL{u},
		Credentials:  linkCreds,
		LocalAccount: pub(id.acc), // bind the federated subjects into the one SEXTANT account
		Hub:          true,
	}}
	n := startPernodeServer(t, opts, id, "pernode-B")
	n.storeDir = storeDir
	n.linkCredsPath = linkCreds
	return n
}

// mintLinkCredsFile mints a SEXTANT-user leaf-link credential from id (the real
// bus's leafLinkPermissions grant) and writes it to a temp creds file, returning
// the path. It is the de-risk analogue of writeLeafArtifacts — the link is just a
// scoped SEXTANT user, signed by the shared account.
func mintLinkCredsFile(t *testing.T, id *identity) string {
	t.Helper()
	j, seed, _, err := id.mintUser("pernode-leaf-link", operatorPermissions())
	if err != nil {
		t.Fatalf("mint leaf-link credential: %v", err)
	}
	creds, err := credsFile(j, seed)
	if err != nil {
		t.Fatalf("format leaf-link credential: %v", err)
	}
	path := filepath.Join(t.TempDir(), "leaf-link.creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatalf("write leaf-link credential: %v", err)
	}
	return path
}

// startPernodeServer boots an embedded server from opts, waits for readiness,
// opens its client listener, and connects an in-process operator JetStream handle
// to it. The operator credential is minted from id (the same path the real bus
// uses for its own connection).
func startPernodeServer(t *testing.T, opts *natsserver.Options, id *identity, name string) *pernodeNode {
	t.Helper()
	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("%s: new server: %v", name, err)
	}
	ns.Start()
	if err := waitReady(t.Context(), ns, 10*time.Second); err != nil {
		ns.Shutdown()
		t.Fatalf("%s: not ready: %v", name, err)
	}
	ns.AcceptLoop(make(chan struct{}))
	if ns.Addr() == nil {
		ns.Shutdown()
		t.Fatalf("%s: client listener failed", name)
	}

	opJWT, opSeed, _, err := id.mintUser(name+"-operator", operatorPermissions())
	if err != nil {
		ns.Shutdown()
		t.Fatalf("%s: mint operator: %v", name, err)
	}
	nc, err := nats.Connect("", nats.InProcessServer(ns),
		nats.UserJWTAndSeed(opJWT, opSeed), nats.Name(name+"-operator"))
	if err != nil {
		ns.Shutdown()
		t.Fatalf("%s: operator connect: %v", name, err)
	}
	domainOpt := jetstream.WithDefaultTimeout(10 * time.Second)
	js, err := jetstream.NewWithDomain(nc, opts.JetStreamDomain, domainOpt)
	if err != nil {
		nc.Close()
		ns.Shutdown()
		t.Fatalf("%s: jetstream: %v", name, err)
	}
	n := &pernodeNode{ns: ns, nc: nc, js: js, domain: opts.JetStreamDomain}
	t.Cleanup(n.shutdown)
	return n
}

// pernodeKV opens (or creates) the named local KV bucket on a node.
func pernodeKV(t *testing.T, n *pernodeNode, bucket string) jetstream.KeyValue {
	t.Helper()
	kv, err := n.js.CreateKeyValue(t.Context(), jetstream.KeyValueConfig{
		Bucket:  bucket,
		Storage: jetstream.FileStorage,
	})
	if err != nil {
		t.Fatalf("create kv %s on %s: %v", bucket, n.domain, err)
	}
	return kv
}

// provisionMirror creates a same-name External KV mirror of the source domain's
// ARTIFACTS bucket on node B. This is the load-bearing config from the spike:
// a Mirror StreamSource with External.APIPrefix == the source domain's JS API
// ($JS.<srcDomain>.API), so the mirror's records are sourced from the peer's
// bucket across the leaf and read locally via the normal KV API.
func provisionMirror(t *testing.T, n *pernodeNode, bucket, srcDomain string) jetstream.KeyValue {
	t.Helper()
	mirror, err := n.js.CreateKeyValue(t.Context(), jetstream.KeyValueConfig{
		Bucket:  bucket, // SAME name as the source bucket — the spike's "same-name mirror"
		Storage: jetstream.FileStorage,
		Mirror: &jetstream.StreamSource{
			Name: "KV_" + bucket, // a KV bucket's backing stream is KV_<bucket>
			External: &jetstream.ExternalStream{
				APIPrefix: "$JS." + srcDomain + ".API",
			},
		},
	})
	if err != nil {
		t.Fatalf("provision same-name External mirror of %s from domain %s: %v", bucket, srcDomain, err)
	}
	return mirror
}

// waitForMirrorValue polls a mirror KV for key == want, up to a deadline. The
// mirror catch-up is asynchronous (push-based), so the test waits for it rather
// than asserting on the first read; failure means the mechanism did not hold.
func waitForMirrorValue(t *testing.T, kv jetstream.KeyValue, key, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		e, err := kv.Get(t.Context(), key)
		if err == nil {
			last = string(e.Value())
			if last == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("mirror did not catch up: key %q = %q, want %q within %v", key, last, want, within)
}

// TestPernodeJSMirrorMechanism is the slice-1 DE-RISK acceptance test. It proves,
// IN-REPO against sextant's real identity/auth + the vendored nats-server, that
// two nodes each running their OWN JetStream domain over a leaf can present a
// peer's ARTIFACTS bucket as a same-name External KV mirror that node B reads
// locally, survives a partition for local reads, and resyncs on reconnect. This
// is the foundation the per-node-JS substrate (step 2) stands on.
func TestPernodeJSMirrorMechanism(t *testing.T) {
	// One operator + one SEXTANT account, shared by BOTH nodes (sextant's single-
	// account model; the spike's config). loadOrCreateIdentity persists keys under
	// a temp store; we reuse the one identity for A and B so they share trust.
	id, err := loadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	nodeA, hubLeafURL := startPernodeHub(t, id, "A")
	nodeB := startPernodeLeaf(t, id, "B", hubLeafURL)

	// Each node owns a LOCAL ARTIFACTS bucket in its own domain. (B's own bucket is
	// distinct from the mirror it holds of A's; we name B's own one ARTIFACTS_B to
	// keep the flat-union step unambiguous within this single-process test.)
	kvA := pernodeKV(t, nodeA, pernodeArtifactsBucket)
	kvBOwn := pernodeKV(t, nodeB, pernodeArtifactsBucket+"_B")

	// Wait for the leaf link to come up on B before provisioning the mirror, so the
	// External source is reachable.
	waitLeafLinkedRaw(t, nodeB.ns, 10*time.Second)

	// === Property 1: A writes -> B reads it through a same-name External mirror ===
	if _, err := kvA.Put(t.Context(), "alpha", []byte("from-A-v1")); err != nil {
		t.Fatalf("A put alpha: %v", err)
	}
	mirrorOnB := provisionMirror(t, nodeB, pernodeArtifactsBucket, "A")
	waitForMirrorValue(t, mirrorOnB, "alpha", "from-A-v1", 5*time.Second)
	t.Log("property 1 OK: B reads A's write through the same-name External mirror")

	// A further write on A also propagates (steady-state catch-up).
	if _, err := kvA.Put(t.Context(), "beta", []byte("from-A-v2")); err != nil {
		t.Fatalf("A put beta: %v", err)
	}
	waitForMirrorValue(t, mirrorOnB, "beta", "from-A-v2", 5*time.Second)

	// === Property 4 (flat-union): B reads its OWN bucket together with the mirror ===
	if _, err := kvBOwn.Put(t.Context(), "gamma", []byte("from-B-local")); err != nil {
		t.Fatalf("B put gamma (own): %v", err)
	}
	if e, err := kvBOwn.Get(t.Context(), "gamma"); err != nil || string(e.Value()) != "from-B-local" {
		t.Fatalf("B own bucket read: val=%q err=%v", valueOf(e), err)
	}
	if e, err := mirrorOnB.Get(t.Context(), "alpha"); err != nil || string(e.Value()) != "from-A-v1" {
		t.Fatalf("B mirror read in union: val=%q err=%v", valueOf(e), err)
	}
	t.Log("property 4 OK: B serves its own bucket and A's mirror as one flat set")

	// === Property 2 + 3: partition -> local reads survive -> reconnect resyncs ===
	// Model the partition the way the spike did and the way offline operation
	// actually happens: node A (the source) STAYS UP throughout; node B (the
	// disconnected node holding the mirror) loses the link. We sever it by bouncing
	// node B on its SAME store — its mirror-stream state persists on disk, so on
	// restart B re-links to A and the mirror's source consumer resumes from its last
	// sequence. CRUCIALLY, A is written WHILE B is down (a true gap), proving the
	// resync catches up writes B never saw — the offline-then-reconnect path.
	bLeafCreds := nodeB.linkCredsPath
	bStore := nodeB.storeDir

	// Sever the link: stop B (leave its store). A keeps serving.
	nodeB.shutdownLeaveStore()
	waitLeafUnlinked(t, nodeA.ns, 5*time.Second)

	// A keeps serving its OWN bucket while B is partitioned away (A's availability).
	if e, err := kvA.Get(t.Context(), "alpha"); err != nil || string(e.Value()) != "from-A-v1" {
		t.Fatalf("partitioned A own read (should serve local): val=%q err=%v", valueOf(e), err)
	}
	// Write to A DURING the partition — the gap B must catch up on reconnect.
	if _, err := kvA.Put(t.Context(), "delta", []byte("from-A-during-partition")); err != nil {
		t.Fatalf("A put delta during partition: %v", err)
	}
	t.Log("property 2 OK: A serves + accepts local writes while B is partitioned away")

	// Bring B back on the SAME store (its mirror-stream state persisted), re-linking
	// to A. The mirror's source consumer resumes and catches up the gap.
	nodeB = restartPernodeLeaf(t, id, "B", hubLeafURL, bStore, bLeafCreds)
	waitLeafLinkedRaw(t, nodeB.ns, 10*time.Second)
	mirrorOnB2, err := nodeB.js.KeyValue(t.Context(), pernodeArtifactsBucket)
	if err != nil {
		t.Fatalf("reopen B's mirror after reconnect: %v", err)
	}

	// === Property 3: the mirror catches up the gap written during the partition ===
	waitForMirrorValue(t, mirrorOnB2, "delta", "from-A-during-partition", 10*time.Second)
	// And the pre-partition values are still readable (no loss).
	waitForMirrorValue(t, mirrorOnB2, "alpha", "from-A-v1", 2*time.Second)
	// B's own bucket also survived the bounce (local durability).
	if e, err := nodeB.js.KeyValue(t.Context(), pernodeArtifactsBucket+"_B"); err == nil {
		waitForMirrorValue(t, e, "gamma", "from-B-local", 2*time.Second)
	} else {
		t.Fatalf("reopen B's own bucket after reconnect: %v", err)
	}
	t.Log("property 3 OK: the mirror resynced the partition gap after reconnect (partition-tolerant)")
}

// valueOf is a nil-safe helper for failure messages.
func valueOf(e jetstream.KeyValueEntry) string {
	if e == nil {
		return "<nil>"
	}
	return string(e.Value())
}

// shutdownLeaveStore stops the node's server + connection WITHOUT clearing its
// store, so a restart on the same StoreDir resumes its JetStream domain state. It
// is the partition primitive: dropping the peer drops the leaf link.
func (n *pernodeNode) shutdownLeaveStore() {
	if n.nc != nil {
		n.nc.Close()
		n.nc = nil
	}
	if n.ns != nil {
		n.ns.Shutdown()
		n.ns.WaitForShutdown()
		n.ns = nil
	}
}

// waitLeafLinkedRaw blocks until ns reports an established leaf link (NumLeafs>0)
// or the deadline passes. Works on either side of the link (the hub counts the
// inbound leaf; the leaf counts its remote). It is the raw-server analogue of
// Bus.WaitLeafLinked, used here because the de-risk harness builds servers
// directly rather than through Bus.
func waitLeafLinkedRaw(t *testing.T, ns *natsserver.Server, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if lz, err := ns.Leafz(&natsserver.LeafzOptions{}); err == nil && lz.NumLeafs > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("leaf link did not come up within %v", within)
}

// waitLeafUnlinked blocks until ns reports NO leaf links (the partition took
// hold) or the deadline passes.
func waitLeafUnlinked(t *testing.T, ns *natsserver.Server, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if lz, err := ns.Leafz(&natsserver.LeafzOptions{}); err == nil && lz.NumLeafs == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("leaf link did not drop within %v", within)
}
