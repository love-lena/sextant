package bus

import (
	"fmt"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// Bus WebSocket listener (ADR-0044): a default-off, loopback-only ws:// listener
// on the embedded hub so a browser dash can connect as a co-equal TypeScript bus
// client (@sextant/sdk over nats.ws), with a short-lived dash-minted scoped
// credential. It is the leaf-listener pattern (ADR-0038) applied to the browser:
// a second listener, off unless its address is set, that binds loopback only and
// rides the operator's external secure transport rather than carrying its own TLS.
//
// The browser presents its bus credential's JWT during the WS upgrade — either as
// the nats.js CONNECT `jwt` field (the path @sextant/sdk's nats.ws dialer uses) or
// as the JWTCookie below. Minting stays at the hub: the dash, a top-level client,
// asks the bus to mint the child credential over clients.register (ADR-0033); the
// browser only carries what the bus already issued.

// websocketJWTCookie is the cookie name a browser may carry its JWT in during the
// WS upgrade. The SDK's nats.ws dialer authenticates with the CONNECT `jwt` field
// (no cookie), so this is the belt-and-braces path, not a requirement; it needs
// TrustedOperators, already installed by serverAuthOptions.
const websocketJWTCookie = "sxtjwt"

// applyWebSocketListener wires a loopback ws:// listener onto opts (ADR-0044) and
// fails CLOSED on an unsafe bind: it binds the configured host:port only when the
// host is loopback. A non-loopback bind would be a routable unencrypted WebSocket
// listener — the one unacceptable configuration — and native wss TLS is not yet
// implemented, so the bus refuses it rather than open it. Loopback is allowed bare
// on purpose: it rides an external secure transport (SSH-R / Tailscale / WireGuard)
// that carries the encryption, exactly the leaf listener's posture. Default-off —
// only called when WebSocketListenAddr is set.
func applyWebSocketListener(opts *natsserver.Options, addr string) error {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bus: --ws-listen %q: %w", addr, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("bus: --ws-listen %q must bind a loopback host (127.0.0.1 / ::1): a non-loopback WebSocket listener would be routable and unencrypted (native wss TLS is a follow-up); bind loopback behind a secure transport (SSH-R / Tailscale / WireGuard)", addr)
	}
	opts.Websocket = natsserver.WebsocketOpts{
		Host: host,
		Port: port,
		// ws:// on loopback: the secure transport is external, the same NoTLS-loopback
		// posture the leaf listener uses. The NATS server requires NoTLS=true when no
		// TLSConfig is set (websocket.go's TLS gate); native wss TLS is a follow-up.
		NoTLS: true,
		// Let a browser present its JWT as a cookie during the WS upgrade (needs
		// TrustedOperators, already installed). The SDK uses the CONNECT jwt field
		// instead, so this is additive, not required.
		JWTCookie: websocketJWTCookie,
	}
	return nil
}
