package bus

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
)

// Per-node JetStream substrate (v0.6 slice 1, the foundation for offline
// operation). The approved design is [[task125-offline-replication-options]];
// the load-bearing mechanism is re-confirmed in-repo by
// TestPernodeJSMirrorMechanism (pernode_mirror_test.go).
//
// What slice 1 IS: each node runs its OWN JetStream domain with a local artifacts
// bucket it reads AND writes, and mirrors each known peer's artifacts bucket
// read-locally over the leaf link via a same-name External KV mirror. A node's
// artifact reads = its own bucket ∪ the peer mirrors (a simple union); its writes
// = its own bucket only. This is the substrate offline operation stands on: a
// node keeps reading (own + last-synced mirrors) and writing (its own) while
// partitioned, and the mirrors catch up on reconnect (partition-tolerant resync).
//
// What slice 1 is NOT (deliberately deferred to later slices): owner-only WRITE
// ENFORCEMENT (the per-client Pub.Deny that makes a peer mirror read-only), the
// flat-namespace resolver + per-entry `writable` flag, the `-n` collision suffix,
// offline MESSAGE union-merge, and the ADR. Slice 1 is the per-node-JS + mirror
// substrate and its confirming test, nothing more.
//
// Additive by construction: with NO NodeID and NO peers (the default), nothing
// here changes — the bus runs the single global ARTIFACTS bucket exactly as
// before. The per-node bucket naming and the mirror provisioning engage only when
// a NodeID (and optionally peers) is configured.

// PeerNode names a peer whose artifacts bucket this node mirrors read-locally
// (v0.6 slice 1). NodeID names the peer's bucket (ARTIFACTS_<NodeID>); Domain is
// the peer's JetStream domain, reached across the leaf via the cross-domain JS API
// ($JS.<Domain>.API). RemoteURL + LinkCreds, when set, make this node solicit a
// leaf link to the peer so the mirror has a transport to source over.
type PeerNode struct {
	// NodeID is the peer's node id — the suffix of its artifacts bucket
	// (ARTIFACTS_<NodeID>). The mirror this node provisions carries the SAME name,
	// which is what makes a same-name External KV mirror readable via the normal KV
	// API (the spike's load-bearing finding).
	NodeID string
	// Domain is the peer's JetStream domain. The mirror sources the peer's bucket
	// across the leaf through that domain's JS API prefix ($JS.<Domain>.API). For a
	// peer that runs its own per-node bus, Domain == the peer's NodeID.
	Domain string
	// RemoteURL is the peer's nats-leaf:// URL this node solicits a leaf link to, so
	// the mirror has a federated path to source the peer's bucket over. Empty means
	// the link is established by some other means (e.g. the peer links to US, or a
	// shared external transport) — the mirror provisioning is the same either way.
	RemoteURL string
	// LinkCreds is the path to the SEXTANT-user credential this node authenticates
	// the leaf link to the peer with. Required when RemoteURL is set. The link must
	// carry mirror-replication traffic, so it is minted with peerLinkPermissions
	// (the federation set plus the JetStream replication surface).
	LinkCreds string
}

// artifactsBucketFor returns the artifacts bucket name a node with the given id
// owns. The empty id is the single-hub case: the original global bucket name, so
// a bus with no NodeID is byte-identical to before this slice. A non-empty id
// gives the per-node bucket ARTIFACTS_<id>, the only bucket that node writes.
func artifactsBucketFor(nodeID string) string {
	if nodeID == "" {
		return sx.BucketArtifacts
	}
	return sx.BucketArtifacts + "_" + nodeID
}

// ownArtifactsBucket is the bucket THIS node owns and writes — the single global
// ARTIFACTS for a hub with no node id, or ARTIFACTS_<NodeID> for a per-node bus.
// Every artifact WRITE (create/update/delete) targets this bucket; only its owner
// node writes it. (Owner-only ENFORCEMENT is slice 2; here a non-owner simply has
// no write path to a peer mirror through this node.)
func (b *Bus) ownArtifactsBucket() string { return artifactsBucketFor(b.nodeID) }

// artifactReadBuckets is the ordered set of buckets an artifact READ unions over:
// this node's own bucket FIRST, then each peer's mirror bucket. The own-first
// order makes the simple read-union deterministic — a name present locally wins
// over a peer mirror of the same name. (The flat-namespace resolver and the
// collision `-n` suffix are slice 2; slice 1's union is intentionally simple.)
func (b *Bus) artifactReadBuckets() []string {
	buckets := []string{b.ownArtifactsBucket()}
	for _, p := range b.peers {
		buckets = append(buckets, artifactsBucketFor(p.NodeID))
	}
	return buckets
}

// artifactBucketForName resolves which read-bucket currently holds name — own
// bucket first, then each peer mirror (v0.6 slice 1, same own-first order as the
// get/list union). An unknown name resolves to the own bucket: where a future
// local create would land, so a watch started before a create still tracks it. For
// the single hub this is always the one global bucket.
func (b *Bus) artifactBucketForName(ctx context.Context, name string) string {
	own := b.ownArtifactsBucket()
	for _, bucket := range b.artifactReadBuckets() {
		if _, _, err := b.backend.Get(ctx, bucket, name); err == nil {
			return bucket
		}
	}
	return own
}

// validateNodeConfig checks the per-node invariants before Start wires anything
// (the fail-loud discipline): a node id and every peer id/domain must be a single
// plain subject token (they are woven into bucket names and JS API subjects), and
// a peer may not collide with this node. It is a no-op for the single-hub default
// (empty NodeID and no peers).
func validateNodeConfig(cfg Config) error {
	// Per-node-JS mode (NodeID set, runs its own JetStream domain) and leaf mode
	// (LeafRemoteURL set, JetStream OFF, engine at the hub) are mutually exclusive —
	// a bus is one or the other. Fail loud rather than silently ignore NodeID on the
	// leaf path (startLeaf never reads it).
	if cfg.NodeID != "" && cfg.LeafRemoteURL != "" {
		return errors.New("bus: NodeID (per-node JetStream mode) and --leaf-remote (leaf mode, JetStream off) are mutually exclusive — a bus runs its own domain OR links to a hub as a leaf, not both")
	}
	if cfg.NodeID == "" {
		if len(cfg.Peers) > 0 {
			return fmt.Errorf("bus: peers are configured without a NodeID (a node that mirrors peers must have its own id)")
		}
		return nil
	}
	if err := validNodeToken(cfg.NodeID, "node id"); err != nil {
		return err
	}
	seen := map[string]bool{cfg.NodeID: true}
	for _, p := range cfg.Peers {
		if err := validNodeToken(p.NodeID, "peer node id"); err != nil {
			return err
		}
		if err := validNodeToken(p.Domain, "peer domain"); err != nil {
			return err
		}
		if seen[p.NodeID] {
			return fmt.Errorf("bus: peer node id %q is duplicated or collides with this node's id", p.NodeID)
		}
		seen[p.NodeID] = true
	}
	return nil
}

// validNodeToken rejects an id/domain that is not a single plain subject token. A
// `.`, a wildcard, or whitespace would corrupt the bucket name or the JS API
// subject the mirror is built from — fail loud rather than provision a malformed
// mirror.
func validNodeToken(s, what string) error {
	if s == "" {
		return fmt.Errorf("bus: %s is empty", what)
	}
	if strings.ContainsAny(s, ".*> \t\r\n/") {
		return fmt.Errorf("bus: %s %q is not a single plain token (no . * > whitespace or /)", what, s)
	}
	return nil
}

// newJetStream opens the bus's JetStream handle, scoped to this node's OWN domain
// when a NodeID is set (v0.6 slice 1). This is LOAD-BEARING: with a leaf-linked
// peer sharing the one SEXTANT account, an UNSCOPED jetstream.New resolves the
// generic $JS.API prefix, which BOTH domains can answer — so a KV write would
// route ambiguously and an artifact written here could land in the peer's bucket
// (the cross-domain leak the de-risk surfaced). Pinning to $JS.<NodeID>.API keeps
// every storage operation on THIS node's domain. The single hub (empty NodeID)
// uses the default unscoped handle — byte-identical to before this slice.
func (b *Bus) newJetStream() (jetstream.JetStream, error) {
	if b.nodeID == "" {
		js, err := jetstream.New(b.opConn)
		if err != nil {
			return nil, fmt.Errorf("bus: jetstream: %w", err)
		}
		return js, nil
	}
	js, err := jetstream.NewWithDomain(b.opConn, b.nodeID)
	if err != nil {
		return nil, fmt.Errorf("bus: jetstream (domain %s): %w", b.nodeID, err)
	}
	return js, nil
}

// provisionArtifactSubstrate provisions this node's own artifacts bucket and a
// same-name External read-mirror of each configured peer's artifacts bucket
// (v0.6 slice 1). Called from bootstrap after the reserved buckets, in place of
// the single global artifacts bucket when a NodeID is set. For the single-hub
// default it provisions exactly the one global ARTIFACTS bucket bootstrap used to
// — no behaviour change.
//
// The peer mirror is the spike-confirmed config: a KV bucket named the SAME as the
// peer's source bucket (ARTIFACTS_<peer>), configured as a Mirror of that bucket's
// backing stream (KV_ARTIFACTS_<peer>) via External.APIPrefix == the peer domain's
// JS API ($JS.<Domain>.API). NATS sources the records across the leaf and serves
// them locally through the normal KV API, so this node reads the peer's artifacts
// even while later partitioned (from the last-synced copy).
func (b *Bus) provisionArtifactSubstrate(ctx context.Context, js jetstream.JetStream) error {
	// This node's own read-write bucket. Same shape as the global ARTIFACTS bucket
	// (history + file storage); only the name differs for a per-node bus.
	if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  b.ownArtifactsBucket(),
		History: sx.ArtifactHistory,
		Storage: jetstream.FileStorage,
	}); err != nil {
		return fmt.Errorf("bus: provision own artifacts bucket %s: %w", b.ownArtifactsBucket(), err)
	}

	// A same-name External read-mirror of each peer's artifacts bucket. Idempotent
	// (CreateOrUpdate): a restart re-uses the existing mirror, which resumes its
	// source consumer and catches up the gap.
	for _, p := range b.peers {
		peerBucket := artifactsBucketFor(p.NodeID)
		if _, err := js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:  peerBucket, // SAME name as the peer's source bucket — required for KV-API readability
			History: sx.ArtifactHistory,
			Storage: jetstream.FileStorage,
			Mirror: &jetstream.StreamSource{
				Name: "KV_" + peerBucket, // a KV bucket's backing stream is KV_<bucket>
				External: &jetstream.ExternalStream{
					APIPrefix: peerJSAPIPrefix(p.Domain),
				},
			},
		}); err != nil {
			return fmt.Errorf("bus: provision mirror of peer %s (domain %s): %w", p.NodeID, p.Domain, err)
		}
	}
	return nil
}

// peerRemoteOpts builds the RemoteLeafOpts that link this node to each peer that
// declares a RemoteURL (v0.6 slice 1), binding the link into the one SEXTANT
// account so the cross-domain JS API + KV subjects federate within it. A peer with
// no RemoteURL contributes no remote (its link is solicited from the other side).
// It fails loud if a RemoteURL is given without LinkCreds — a link with no
// credential never authenticates to a JWT-auth peer.
func peerRemoteOpts(peers []PeerNode, accountKey string) ([]*natsserver.RemoteLeafOpts, error) {
	var remotes []*natsserver.RemoteLeafOpts
	for _, p := range peers {
		if p.RemoteURL == "" {
			continue
		}
		if p.LinkCreds == "" {
			return nil, fmt.Errorf("bus: peer %s has a RemoteURL but no LinkCreds (a leaf link needs a credential)", p.NodeID)
		}
		u, err := url.Parse(p.RemoteURL)
		if err != nil {
			return nil, fmt.Errorf("bus: peer %s: parse remote url %q: %w", p.NodeID, p.RemoteURL, err)
		}
		if fi, err := os.Stat(p.LinkCreds); err != nil {
			return nil, fmt.Errorf("bus: peer %s: link credential %s: %w", p.NodeID, p.LinkCreds, err)
		} else if fi.IsDir() {
			return nil, fmt.Errorf("bus: peer %s: link credential %s is a directory", p.NodeID, p.LinkCreds)
		}
		remotes = append(remotes, &natsserver.RemoteLeafOpts{
			URLs:         []*url.URL{u},
			Credentials:  p.LinkCreds,
			LocalAccount: accountKey,
			Hub:          true,
			// Defense-in-depth on the transport: also deny the bare KV message space
			// ($KV.>) at the leaf link itself. Write isolation is primarily held by the
			// peer-link CREDENTIAL being replication-only (peerLinkPermissions grants no
			// wire API + no bare $KV), so neither node's KV writes nor client calls
			// cross. This transport-level deny is a second, credential-independent fence:
			// it keeps a node's own-bucket KV writes ($KV.<bucket>.>) domain-local even
			// if a future change widens the credential. The mirror is UNAFFECTED — it
			// sources via the domain-qualified $JS.<domain>.API.> + the $JS.M.> / $JSC.R.>
			// replication transport, never bare $KV.
			DenyImports: []string{kvFederationSubject},
			DenyExports: []string{kvFederationSubject},
		})
	}
	return remotes, nil
}

// peerJSAPIPrefix is the cross-domain JetStream API prefix for a peer domain
// ($JS.<domain>.API) — the subject a same-name External mirror sources the peer's
// bucket through. It is the one place the prefix shape lives.
func peerJSAPIPrefix(domain string) string { return "$JS." + domain + ".API" }

// kvFederationSubject is the bare KV message space the per-node leaf link denies in
// both directions as defense-in-depth (see peerRemoteOpts): a node's KV writes land
// here, so a transport-level deny keeps them domain-local independent of the
// credential. Mirror replication rides the domain-qualified $JS.<domain>.API + the
// $JS.M.> / $JSC.R.> replication transport instead, so denying this does not break it.
const kvFederationSubject = "$KV.>"

// MintPeerLinkCreds mints a SEXTANT-user credential a PEER uses to link its leaf to
// THIS node (v0.6 slice 1). It is the per-node analogue of the hub's leaf-link
// credential, but minted with peerLinkPermissions SCOPED TO THIS NODE'S OWN domain —
// because the only JetStream replication the link carries is a peer sourcing THIS
// node's artifacts bucket (over $JS.<this-node-domain>.API.>). The signing keys
// never leave the bus (it is the minter), so this is a scoped minted credential,
// not key material. A leaf bus has no signing identity and returns a clean error.
//
// Scoping to b.nodeID (not all domains) keeps the trust boundary tight: a remote
// box holds this credential, so it is scoped exactly to the domain it legitimately
// sources and nothing more — the #174 leaf-link lesson (a credential that carries
// traffic must still be the narrowest grant that carries it).
func (b *Bus) MintPeerLinkCreds(name string) (creds string, err error) {
	if b.isLeaf() {
		return "", fmt.Errorf("bus: minting a peer-link credential is unavailable on a leaf — mint at the node (it holds no signing key)")
	}
	j, seed, _, err := b.ident.mintUser(name, peerLinkPermissions(b.nodeID))
	if err != nil {
		return "", fmt.Errorf("bus: mint peer-link credential: %w", err)
	}
	c, err := credsFile(j, seed)
	if err != nil {
		return "", fmt.Errorf("bus: format peer-link credential: %w", err)
	}
	return c, nil
}

// peerLinkPermissions is the leaf-link grant for a per-node bus: a JetStream
// replication-ONLY credential, scoped to the domain(s) the link legitimately sources
// (v0.6 slice 1). The grant is exactly the cross-domain JetStream API of each named
// source domain ($JS.<srcDomain>.API.>) plus the two domain-independent replication
// transport subjects a same-name External KV mirror needs ($JS.M.> push delivery,
// $JSC.R.> consumer-create reply). Nothing else.
//
// It is deliberately NARROW: NOT operatorPermissions() (it carries traffic, not
// authority); NOT the all-domains $JS.> wildcard (a peer-link credential lives on a
// remote box — the #174 trust boundary — so it gets exactly the source domain(s) it
// needs); NOT the bare $KV.> space (the mirror never needs it; see kvFederationSubject);
// and — critically — NOT the wire-API federation set (sx.api.>/sx.deliver.>/...) that
// leafLinkPermissions grants for an ADR-0038 engine-less leaf. A per-node bus runs its
// OWN engine; if the link carried the wire API, both engines would serve each other's
// client calls and a peer's create would land in this node's own bucket (the
// cross-node write leak — TestPernodeSubstrateCrossNodeWriteIsolation). (Owner-only
// WRITE enforcement — denying a non-owner's write-through to a peer's bucket — is slice
// 2's per-client Pub.Deny; this grant is about letting REPLICATION flow, scoped.)
//
// srcDomains is the set of JetStream domains the link sources (a node minting a
// credential for a peer to link in passes its OWN domain — the one the peer
// mirrors). An empty/blank entry is skipped: a node with no domain has no
// cross-domain JS surface to grant.
func peerLinkPermissions(srcDomains ...string) jwt.Permissions {
	var p jwt.Permissions
	// A per-node peer link is NOT the ADR-0038 wire-API leaf. There, an engine-less
	// leaf federates the per-client wire API (sx.api.>/sx.deliver.>/_INBOX.>/sx.hb.>)
	// so its local agents reach the HUB's engine. Here EACH node runs its OWN engine
	// and serves its OWN clients; the only thing that crosses a per-node link is
	// artifact DATA replication for the mirror. So this grant deliberately does NOT
	// inherit leafLinkPermissions' wire-API set — if it did, BOTH nodes' engines would
	// subscribe sx.api.*.> across the link and DOUBLE-SERVE each other's client calls,
	// silently writing a peer's create into THIS node's own bucket (a cross-node write
	// leak; see TestPernodeSubstrateCrossNodeWriteIsolation). The peer link carries
	// JetStream replication and nothing else.
	for _, d := range srcDomains {
		if d == "" {
			continue
		}
		// Exactly this source domain's cross-domain JetStream API — the mirror's
		// source-consumer CREATE/INFO requests ride $JS.<srcDomain>.API.> (the request
		// subject has JSApiPrefix rewritten to the External.APIPrefix; nats-server
		// stream.go generateSubject).
		jsAPI := peerJSAPIPrefix(d) + ".>"
		p.Pub.Allow = append(p.Pub.Allow, jsAPI)
		p.Sub.Allow = append(p.Sub.Allow, jsAPI)
	}
	// The mirror's PUSH DELIVERY subject. A same-name External KV mirror with only an
	// APIPrefix (no DeliverPrefix) takes nats-server's default mirror deliver subject
	// $JS.M.<token> (stream.go: deliverSubject = syncSubject("$JS.M")). The sourced
	// records are pushed there and flow-control acks ride the same channel; without it
	// the source consumer is created but never delivers, so the mirror stays empty
	// (the bug that was masked by the wire-API double-serve). It is a single
	// replication-transport prefix shared across domains — there is no per-domain form
	// — far narrower than the all-domains $JS.> the #174 trust boundary forbids on a
	// remote-held credential. (Sources, not mirrors, use $JS.S.<token>; slice 1
	// provisions only mirrors, so $JS.S is not granted.)
	p.Pub.Allow = append(p.Pub.Allow, jsMirrorDelivery)
	p.Sub.Allow = append(p.Sub.Allow, jsMirrorDelivery)
	// The cross-domain consumer-create REPLY channel ($JSC.R.>): the mirror's
	// CreateConsumer request replies on an infoReplySubject() = $JSC.R.<token>
	// (nats-server jetstream_cluster.go), NOT under any $JS.<domain>.API prefix.
	// Without it the create round-trip never completes. Single internal-replication
	// prefix, no per-domain form; carries only replication control, never KV data.
	p.Pub.Allow = append(p.Pub.Allow, jsReplicationDelivery)
	p.Sub.Allow = append(p.Sub.Allow, jsReplicationDelivery)
	// Defensively deny the bare KV message space ($KV.>) on BOTH directions: the link
	// never needs it (the mirror sources via the domain API + $JS.M delivery, never
	// bare $KV), and an explicit deny keeps a node's own-bucket KV writes
	// ($KV.<bucket>.>) domain-local even if a future change widens the allow set.
	p.Pub.Deny = append(p.Pub.Deny, kvFederationSubject)
	p.Sub.Deny = append(p.Sub.Deny, kvFederationSubject)
	return p
}

// jsMirrorDelivery is the default push-delivery subject a cross-domain KV MIRROR
// (APIPrefix-only, no DeliverPrefix) receives its sourced records on:
// $JS.M.<token> (nats-server stream.go setupMirrorConsumer). A peer-link credential
// must carry it or the mirror's source consumer never delivers.
const jsMirrorDelivery = "$JS.M.>"

// jsReplicationDelivery is the JetStream cross-domain consumer-create reply prefix
// ($JSC.R.>) a peer-link credential must carry for a same-name External mirror to
// complete its source-consumer create round-trip (see peerLinkPermissions). It is
// internal replication control, not client/KV data.
const jsReplicationDelivery = "$JSC.R.>"
