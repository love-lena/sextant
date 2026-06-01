package natsboot

import (
	"fmt"
	"io"
	"strconv"
)

// renderConfig writes a nats-server .conf representation of cfg to w.
// The format is NATS' native key/value syntax. We intentionally hand-roll
// the renderer rather than pulling in the nats-server library so natsboot
// can stay minimal and the output stays human-inspectable.
//
// cfg must be the result of Config.validateAndFill — no defaulting is
// performed here.
func renderConfig(w io.Writer, cfg Config) error {
	// All values that hold a path or user-controlled string get
	// quote-escaped so embedded quotes/newlines cannot break the
	// generated file.
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}

	if err := write("server_name: %s\n", quoteString(cfg.ServerName)); err != nil {
		return err
	}
	if err := write("listen: %s\n", quoteString(cfg.ListenHost+":"+strconv.Itoa(cfg.ListenPort))); err != nil {
		return err
	}
	// nats-server v2.14 runs in strict-config mode and rejects unknown
	// keys. Stick to the minimum set: server_name, listen, jetstream,
	// authorization. Logging defaults (stderr) are fine; LogFile in the
	// Config redirects stdout/stderr at the process level instead.
	if err := write("jetstream {\n  store_dir: %s\n}\n", quoteString(cfg.DataDir)); err != nil {
		return err
	}
	if err := renderAuthorization(w, cfg); err != nil {
		return err
	}
	return nil
}

// renderAuthorization writes the role-scoped `authorization { users = [...] }`
// block (control-plane RFC §5.7, feat-ctl-f0). The single unrestricted
// account is gone: the daemon is now the BROKER-ENFORCED sole publisher to
// agent inboxes.
//
//   - daemon   — publish ">"  / subscribe ">". The daemon's in-process
//     connection (RPC + MCP + reconciler + shipper) is the only principal
//     the broker permits to publish to agents.*.inbox.
//   - operator — publish sextant.rpc.* (RPC requests) and _INBOX.> (NATS
//     request/reply reply subjects) ONLY; subscribe agents.*.frames,
//     agents.*.lifecycle, the RPC reply inboxes, and the KV/JS API the
//     read-path TUIs use. CANNOT publish to agents.*.inbox — the side
//     door is closed structurally, not by convention.
//   - sidecar  — publish agents.*.{frames,heartbeat,lifecycle} and
//     _INBOX.>; subscribe agents.*.inbox + _INBOX.>. Per-uuid narrowing
//     is the future per-incarnation NATS-JWT work (RFC §5.7); F0 scopes
//     the sidecar off the inbox-publish and rpc surfaces at the broker.
//
// The JetStream/KV management API ($JS.API.>) is allowed for operator and
// sidecar so existing read-path TUIs (KV-backed) keep working off the
// gauntlet (RFC §5.7: "reads stay off the gauntlet").
func renderAuthorization(w io.Writer, cfg Config) error {
	write := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}
	if err := write("authorization {\n  users = [\n"); err != nil {
		return err
	}
	// daemon — sole publisher; full subscribe.
	if err := write(
		"    { user: %s, password: %s, permissions: { publish: { allow: [\">\"] }, subscribe: { allow: [\">\"] } } }\n",
		quoteString(cfg.DaemonUser),
		quoteString(cfg.DaemonPassword),
	); err != nil {
		return err
	}
	// operator — RPC requests + reply inboxes only on publish; NOT inboxes.
	if err := write(
		"    { user: %s, password: %s, permissions: { publish: { allow: [%s] }, subscribe: { allow: [%s] } } }\n",
		quoteString(cfg.OperatorUser),
		quoteString(cfg.OperatorPassword),
		joinQuoted(operatorPublishAllow),
		joinQuoted(operatorSubscribeAllow),
	); err != nil {
		return err
	}
	// sidecar — per-agent frames/heartbeat/lifecycle publish; inbox subscribe.
	if err := write(
		"    { user: %s, password: %s, permissions: { publish: { allow: [%s] }, subscribe: { allow: [%s] } } }\n",
		quoteString(cfg.SidecarUser),
		quoteString(cfg.SidecarPassword),
		joinQuoted(sidecarPublishAllow),
		joinQuoted(sidecarSubscribeAllow),
	); err != nil {
		return err
	}
	if err := write("  ]\n}\n"); err != nil {
		return err
	}
	return nil
}

// The role-scoped subject allow-lists. These are the broker-enforced
// front door (RFC §5.7). Keep them in sync with the principals table in
// renderAuthorization's doc comment and conventions/operator-experience.md.
var (
	// operatorPublishAllow: RPC requests + the ephemeral NATS reply
	// subjects request/reply provisions (pkg/client uses nats.NewInbox,
	// which mints _INBOX.<token>) + the JetStream/KV API the read-path
	// TUIs and pkg/client.Subscribe call (a JS API request is a *publish*
	// to $JS.API.>; replies come back on the _INBOX). Deliberately omits
	// agents.*.inbox — the front door (RFC §5.7).
	operatorPublishAllow = []string{
		"sextant.rpc.*",
		"_INBOX.>",
		"$JS.API.>",
	}
	// operatorSubscribeAllow: the diagnostic/read streams the CLI + TUIs
	// consume, the RPC + JS reply inboxes, and the raw KV value subjects
	// the KV-backed read TUIs watch (reads stay off the gauntlet).
	operatorSubscribeAllow = []string{
		"agents.*.frames",
		"agents.*.lifecycle",
		"_INBOX.>",
		"$JS.API.>",
		"$KV.>",
	}
	// sidecarPublishAllow: the per-agent streams the sidecar emits onto,
	// reply inboxes, and the JS/KV API for its session-snapshot writes.
	// Omits sextant.rpc.* (control is the operator/daemon lane) and
	// agents.*.inbox.
	sidecarPublishAllow = []string{
		"agents.*.frames",
		"agents.*.heartbeat",
		"agents.*.lifecycle",
		"_INBOX.>",
		"$JS.API.>",
	}
	// sidecarSubscribeAllow: its own inbox (where the daemon delivers
	// prompts) + reply inboxes + the JS/KV subjects used for session
	// snapshot reads.
	sidecarSubscribeAllow = []string{
		"agents.*.inbox",
		"_INBOX.>",
		"$JS.API.>",
		"$KV.>",
	}
)

// joinQuoted renders a subject allow-list as a comma-separated sequence of
// quoted strings suitable for a NATS `allow: [...]` array body.
func joinQuoted(subjects []string) string {
	out := make([]byte, 0, len(subjects)*16)
	for i, s := range subjects {
		if i > 0 {
			out = append(out, ',', ' ')
		}
		out = append(out, quoteString(s)...)
	}
	return string(out)
}

// quoteString returns s wrapped in double-quotes with embedded quotes and
// backslashes escaped. NATS' config parser accepts the standard
// JSON-like quoting we need.
func quoteString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"':
			out = append(out, '\\', c)
		case '\n':
			out = append(out, '\\', 'n')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, c)
		}
	}
	out = append(out, '"')
	return string(out)
}
