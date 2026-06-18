package bus

import (
	"strings"
	"testing"
)

// TestPeerLinkPermissionsScoped pins the trust-boundary scoping (v0.6 slice 1, the
// #174 leaf-link lesson): the peer-link credential is granted EXACTLY the named
// source domain's cross-domain JetStream API ($JS.<srcDomain>.API.>) plus the two
// domain-INDEPENDENT replication transport subjects the mirror needs (the
// consumer-create reply $JSC.R.> and the mirror push-delivery $JS.M.>) — NOT the
// all-domains $JS.> wildcard, NOT operatorPermissions, NOT the bare $KV.> space, and
// NOT the wire-API federation set (sx.api.>/sx.deliver.>, whose federation would let
// the two nodes double-serve each other's client calls — the cross-node write leak).
// A remote box holds this credential, so it must be the narrowest grant that carries
// mirror replication and ONLY mirror replication.
func TestPeerLinkPermissionsScoped(t *testing.T) {
	p := peerLinkPermissions("A")

	all := append(append([]string(nil), p.Pub.Allow...), p.Sub.Allow...)

	// It MUST grant exactly A's domain JS API — scoped, present on pub and sub.
	wantScoped := peerJSAPIPrefix("A") + ".>"
	if !containsExact(p.Pub.Allow, wantScoped) || !containsExact(p.Sub.Allow, wantScoped) {
		t.Errorf("peer-link grant must allow the scoped %q on pub+sub; got pub=%v sub=%v", wantScoped, p.Pub.Allow, p.Sub.Allow)
	}
	// It MUST grant the two replication transport subjects (or the mirror never
	// completes its consumer create / never receives sourced records).
	for _, want := range []string{jsReplicationDelivery, jsMirrorDelivery} {
		if !containsExact(p.Pub.Allow, want) || !containsExact(p.Sub.Allow, want) {
			t.Errorf("peer-link grant must allow the replication transport %q on pub+sub; got pub=%v sub=%v", want, p.Pub.Allow, p.Sub.Allow)
		}
	}
	// It MUST NOT over-grant the all-domains JS wildcard or the bare KV space.
	for _, subj := range all {
		if subj == "$JS.>" {
			t.Errorf("peer-link grant must NOT include the all-domains $JS.> wildcard (over-grant on a remote-held credential); got %v", all)
		}
		if subj == kvFederationSubject || subj == "$KV.>" {
			t.Errorf("peer-link grant must NOT federate the bare $KV space (the cross-domain write leak); got %v", all)
		}
	}
	// It MUST NOT carry the wire API: a per-node peer link replicates artifact DATA
	// only; granting sx.api.>/sx.deliver.> would make both nodes' engines double-serve
	// each other's client calls (the write-isolation leak). The grant is JS-only — it
	// holds no sx.* subject at all.
	for _, subj := range all {
		if strings.HasPrefix(subj, "sx.") {
			t.Errorf("peer-link grant must NOT include the wire-API/federation set (a per-node link is replication-only, not a v0.5 engine-less leaf); got %v", all)
		}
	}

	// A second source domain adds exactly that domain's API — still scoped, no
	// wildcard — on BOTH pub and sub (the link carries replication in both directions).
	p2 := peerLinkPermissions("A", "C")
	wantC := peerJSAPIPrefix("C") + ".>"
	if !containsExact(p2.Pub.Allow, wantC) || !containsExact(p2.Sub.Allow, wantC) {
		t.Errorf("a second source domain must add its scoped API on pub+sub; got pub=%v sub=%v", p2.Pub.Allow, p2.Sub.Allow)
	}

	// A blank domain is skipped (a node with no domain has no cross-domain surface) —
	// on BOTH pub and sub, the ONLY $JS subjects are the domain-independent replication
	// transport ($JSC.R.> reply + $JS.M.> mirror delivery); no per-domain $JS.<domain>
	// grant appears.
	pBlank := peerLinkPermissions("")
	for _, list := range [][]string{pBlank.Pub.Allow, pBlank.Sub.Allow} {
		for _, subj := range list {
			if strings.HasPrefix(subj, "$JS.") && subj != jsReplicationDelivery && subj != jsMirrorDelivery {
				t.Errorf("a blank source domain must add no per-domain $JS.<domain> grant; got %v", list)
			}
		}
	}
}

// TestValidateNodeConfigLeafExclusion pins the fail-loud invariant (v0.6 slice 1):
// per-node-JS mode (NodeID set) and leaf mode (LeafRemoteURL set, JetStream off) are
// mutually exclusive — Start must reject the combination rather than silently ignore
// NodeID on the leaf path.
func TestValidateNodeConfigLeafExclusion(t *testing.T) {
	err := validateNodeConfig(Config{NodeID: "A", LeafRemoteURL: "nats-leaf://hub:7422"})
	if err == nil {
		t.Fatal("NodeID + LeafRemoteURL must be rejected (per-node mode and leaf mode are mutually exclusive)")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should explain the mutual exclusion, got: %v", err)
	}

	// Each alone is valid.
	if err := validateNodeConfig(Config{NodeID: "A"}); err != nil {
		t.Errorf("NodeID alone is valid: %v", err)
	}
	if err := validateNodeConfig(Config{LeafRemoteURL: "nats-leaf://hub:7422"}); err != nil {
		t.Errorf("LeafRemoteURL alone is valid (leaf mode): %v", err)
	}
	// The single-hub default is valid.
	if err := validateNodeConfig(Config{}); err != nil {
		t.Errorf("the single-hub default is valid: %v", err)
	}
}

// containsExact reports whether s contains v exactly.
func containsExact(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
