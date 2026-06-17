package bus

import (
	"strings"
	"testing"
)

// TestPeerLinkPermissionsScoped pins the trust-boundary scoping (v0.6 slice 1, the
// #174 leaf-link lesson): the peer-link credential is granted EXACTLY the named
// source domain's cross-domain JetStream API ($JS.<srcDomain>.API.>) plus the
// replication delivery channel — NOT the all-domains $JS.> wildcard, NOT
// operatorPermissions, and NOT the bare $KV.> space (whose federation is the
// cross-domain write leak). A remote box holds this credential, so it must be the
// narrowest grant that carries replication.
func TestPeerLinkPermissionsScoped(t *testing.T) {
	p := peerLinkPermissions("A")

	all := append(append([]string(nil), p.Pub.Allow...), p.Sub.Allow...)

	// It MUST grant exactly A's domain JS API — scoped, present on pub and sub.
	wantScoped := peerJSAPIPrefix("A") + ".>"
	if !containsExact(p.Pub.Allow, wantScoped) || !containsExact(p.Sub.Allow, wantScoped) {
		t.Errorf("peer-link grant must allow the scoped %q on pub+sub; got pub=%v sub=%v", wantScoped, p.Pub.Allow, p.Sub.Allow)
	}
	// It MUST grant the replication delivery channel (or the mirror never catches up).
	if !containsExact(p.Pub.Allow, jsReplicationDelivery) || !containsExact(p.Sub.Allow, jsReplicationDelivery) {
		t.Errorf("peer-link grant must allow the replication delivery %q on pub+sub; got pub=%v sub=%v", jsReplicationDelivery, p.Pub.Allow, p.Sub.Allow)
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

	// A second source domain adds exactly that domain's API — still scoped, no wildcard.
	p2 := peerLinkPermissions("A", "C")
	if !containsExact(p2.Pub.Allow, peerJSAPIPrefix("C")+".>") {
		t.Errorf("a second source domain must add its scoped API; got %v", p2.Pub.Allow)
	}

	// A blank domain is skipped (a node with no domain has no cross-domain surface).
	pBlank := peerLinkPermissions("")
	for _, subj := range pBlank.Pub.Allow {
		if strings.HasPrefix(subj, "$JS.") && subj != jsReplicationDelivery {
			t.Errorf("a blank source domain must add no $JS.<domain> grant; got %v", pBlank.Pub.Allow)
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
