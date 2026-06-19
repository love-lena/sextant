package sextant

import (
	"net"
	"net/url"
	"time"

	"github.com/love-lena/sextant/protocol/conninfo"
)

// dialReResolveTimeout bounds a single dial. A custom dialer bypasses
// nats.Options.Timeout, so we set our own; 2s matches NATS's default connect
// timeout.
const dialReResolveTimeout = 2 * time.Second

// reResolveDialer is a nats CustomDialer that re-reads the bus discovery file
// (bus.json) on EVERY dial and connects to the URL recorded there, overriding
// the address NATS would otherwise reuse from its boot-time server list.
//
// This is what lets a live client follow a bus that restarts on a DIFFERENT
// port. The bus tries to keep its address across restarts (it reuses its
// previous port if that port is free, else falls back to a random one), so a
// port change is possible — e.g. when the old listener has not yet been released
// at boot. NATS auto-reconnect (MaxReconnects(-1)) keeps dialing the boot
// address forever and never re-reads discovery; without this dialer a port
// change strands every live client on the dead port (the v0.5.1 bus-restart
// incident). With it, each reconnect dial resolves the current port and the
// ReconnectHandler re-establishes the subscription relays (ADR-0027) on the new
// connection — no client restart, no context surgery.
//
// It is attached ONLY when the URL was resolved from a discovery file; a caller
// that pinned Options.URL explicitly is dialed as-is (its choice is respected).
// If the discovery file is missing or unreadable at dial time, the dialer falls
// back to the address NATS passed (the last known good), so a transient read
// error never makes a reconnect worse than today's behaviour.
//
// NOTE (future-surface): the dial target can differ from the address in NATS's
// server pool. That is fine on the current bus — a single loopback, plaintext
// connection (127.0.0.1, no TLS). If the bus ever rides TLS (the ADR-0038 leaf
// TLS follow-up), TLS hostname verification keys off the server-pool name, not
// the dialed address, so re-resolving to a different host would need the verify
// host wired through too. Re-resolving the PORT (the case this fixes) on the same
// loopback host is unaffected.
type reResolveDialer struct {
	connInfoPath string
	timeout      time.Duration
}

// Dial implements nats.CustomDialer. address is the host:port NATS chose from its
// server pool (the boot URL); we override it with the live discovery URL when one
// is readable.
func (d *reResolveDialer) Dial(network, address string) (net.Conn, error) {
	target := address
	if hp := liveHostPort(d.connInfoPath); hp != "" {
		target = hp
	}
	return (&net.Dialer{Timeout: d.timeout}).Dial(network, target)
}

// liveHostPort reads the discovery file and returns the current bus host:port,
// or "" if it cannot be read or parsed (so the caller keeps the address NATS
// chose — never a regression on the existing reconnect path).
func liveHostPort(connInfoPath string) string {
	info, err := conninfo.Read(connInfoPath)
	if err != nil || info.URL == "" {
		return ""
	}
	u, err := url.Parse(info.URL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}
