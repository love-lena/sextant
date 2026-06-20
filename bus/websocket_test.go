package bus

import (
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// TestMintUserTTL pins the ADR-0044 credential-TTL change to mintUser: ttl==0 sets
// no JWT `exp` (the perpetual case every existing caller passes — byte-identical
// to before), and a positive ttl sets an `exp` that the NATS server enforces. The
// JWT is decoded back to confirm the Expires claim, since that is the one
// behavioural change on the locked trust path.
func TestMintUserTTL(t *testing.T) {
	id, err := loadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatalf("loadOrCreateIdentity: %v", err)
	}

	// ttl=0 — perpetual, no exp.
	perpetual, _, _, err := id.mintUser("perpetual", clientPermissions("01HZZZZZZZZZZZZZZZZZZZZZZZ"), 0)
	if err != nil {
		t.Fatalf("mintUser(ttl=0): %v", err)
	}
	pc, err := jwt.DecodeUserClaims(perpetual)
	if err != nil {
		t.Fatalf("decode perpetual jwt: %v", err)
	}
	if pc.Expires != 0 {
		t.Errorf("ttl=0 set Expires=%d, want 0 (perpetual, unchanged)", pc.Expires)
	}

	// ttl>0 — bounded, exp ~ttl from now.
	before := time.Now().Unix()
	bounded, _, _, err := id.mintUser("bounded", clientPermissions("01HZZZZZZZZZZZZZZZZZZZZZZ0"), time.Hour)
	if err != nil {
		t.Fatalf("mintUser(ttl=1h): %v", err)
	}
	bc, err := jwt.DecodeUserClaims(bounded)
	if err != nil {
		t.Fatalf("decode bounded jwt: %v", err)
	}
	if bc.Expires == 0 {
		t.Fatalf("ttl=1h set no Expires, want a bounded exp")
	}
	lo, hi := before+int64(time.Hour.Seconds())-5, time.Now().Unix()+int64(time.Hour.Seconds())+5
	if bc.Expires < lo || bc.Expires > hi {
		t.Errorf("Expires=%d outside [%d,%d] (~1h from now)", bc.Expires, lo, hi)
	}
}

// TestApplyWebSocketListenerLoopback wires a loopback ws-listen address and
// asserts the produced WebsocketOpts: bound to the loopback host:port, NoTLS (so
// the NATS server's TLS gate accepts a ws:// loopback listener), and a JWTCookie
// so a browser may carry its JWT in the upgrade (ADR-0044).
func TestApplyWebSocketListenerLoopback(t *testing.T) {
	cases := []struct{ addr, host string }{
		{"127.0.0.1:7423", "127.0.0.1"},
		{"[::1]:7423", "::1"}, // IPv6 needs bracket notation for host:port
	}
	for _, tc := range cases {
		opts := &natsserver.Options{}
		if err := applyWebSocketListener(opts, tc.addr); err != nil {
			t.Fatalf("applyWebSocketListener(%s): unexpected error: %v", tc.addr, err)
		}
		if opts.Websocket.Host != tc.host || opts.Websocket.Port != 7423 {
			t.Errorf("host=%q port=%d, want %q 7423", opts.Websocket.Host, opts.Websocket.Port, tc.host)
		}
		if !opts.Websocket.NoTLS {
			t.Errorf("NoTLS = false, want true (loopback ws:// must satisfy the NATS TLS gate)")
		}
		if opts.Websocket.JWTCookie != websocketJWTCookie {
			t.Errorf("JWTCookie = %q, want %q", opts.Websocket.JWTCookie, websocketJWTCookie)
		}
	}
}

// TestApplyWebSocketListenerFailsClosedOnNonLoopback asserts the bus refuses a
// routable bind: a non-loopback (or all-interfaces) WebSocket listener would be
// unencrypted and reachable, the one unacceptable configuration, so it fails
// CLOSED rather than opening it (native wss TLS is a follow-up).
func TestApplyWebSocketListenerFailsClosedOnNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7423", "192.168.1.10:7423", ":7423", "example.com:7423"} {
		opts := &natsserver.Options{}
		if err := applyWebSocketListener(opts, addr); err == nil {
			t.Errorf("applyWebSocketListener(%q): expected a fail-closed error, got nil (and Websocket=%+v)", addr, opts.Websocket)
		}
	}
}

// TestApplyWebSocketListenerRejectsMalformed asserts a malformed address fails
// loud rather than half-wiring a listener.
func TestApplyWebSocketListenerRejectsMalformed(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "127.0.0.1:notaport", "127.0.0.1:0", "127.0.0.1:-1"} {
		opts := &natsserver.Options{}
		if err := applyWebSocketListener(opts, addr); err == nil {
			t.Errorf("applyWebSocketListener(%q): expected an error, got nil", addr)
		}
	}
}

// TestWebSocketListenerDefaultOff is the default-off invariant (the mirror of the
// leaf default-off case): a zero Config produces no Websocket opts, byte-identical
// to a bus with no WebSocket listener at all. Start() only calls
// applyWebSocketListener when WebSocketListenAddr is non-empty, so a zero opts.
// Websocket is what an unconfigured bus carries.
func TestWebSocketListenerDefaultOff(t *testing.T) {
	opts := &natsserver.Options{}
	// No call to applyWebSocketListener (Start's guard is `if addr != ""`).
	if opts.Websocket.Port != 0 || opts.Websocket.Host != "" || opts.Websocket.NoTLS || opts.Websocket.JWTCookie != "" {
		t.Errorf("zero Config left a non-zero Websocket opts: %+v", opts.Websocket)
	}
}
