package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// Leaf-node federation (ADR-0038): a remote box runs a local bus in LEAF mode
// that links to the hub over a single SEXTANT account. The per-client wire-API
// subjects (sx.api.<id>.>, sx.deliver.<id>.>, _INBOX.<id>.>, sx.hb.<id>)
// federate within that one account; JetStream (the engine — MESSAGES, ARTIFACTS,
// sx_* KV) stays 100% on the hub. A local agent connects to the leaf with its
// hub-minted credential; the leaf enforces that credential's per-client perms
// LOCALLY (so a publish to a foreign id is rejected at the leaf) and federates
// the call to the hub, which reads the author from the subject token and stamps
// it — trustworthy precisely because the leaf already enforced the credential.
//
// The trust model's key-custody half: the leaf installs the hub's PUBLIC trust
// bundle (operator + SEXTANT + system account JWTs, plus the SEXTANT account
// public key) but never any signing SEED. So the leaf can authenticate the
// link and per-client credentials and resolve the accounts — yet it cannot
// mint a user JWT (minting needs the account seed, which stays at the hub).
// Minting stays at the hub by construction, which keeps the hub's
// subject-derived author stamp the single source of identity.

// LeafBundleFile and LeafLinkCredsFile are the two files `sextant up
// --leaf-listen` writes into the store so an operator can carry them to the
// remote box (ADR-0038). The bundle is public trust material (safe to copy
// in the clear); the link credential is a secret (owner-only).
const (
	LeafBundleFile    = "leaf-bundle.json"
	LeafLinkCredsFile = "leaf-link.creds"
)

// LeafBundlePath and LeafLinkCredsPath are the store paths for the two
// leaf-listen artifacts.
func LeafBundlePath(storeDir string) string    { return filepath.Join(storeDir, LeafBundleFile) }
func LeafLinkCredsPath(storeDir string) string { return filepath.Join(storeDir, LeafLinkCredsFile) }

// leafBundle is the hub's PUBLIC trust material a leaf needs to join (ADR-0038):
// the encoded operator/account/system JWTs (each self-contained, signed by the
// hub operator) and the SEXTANT + system account public keys. It carries NO
// signing seed — that is the whole point, so a leaf built from it cannot mint.
type leafBundle struct {
	OperatorJWT string `json:"operator_jwt"`
	AccountJWT  string `json:"account_jwt"`
	SystemJWT   string `json:"system_jwt"`
	AccountKey  string `json:"account_key"` // SEXTANT account public key
	SystemKey   string `json:"system_key"`  // system account public key
}

// buildLeafBundle assembles the hub's public trust bundle from its identity.
// Each JWT re-encodes the hub's claims (operator self-signed; the two accounts
// signed by the operator) — exactly what serverAuthOptions installs locally —
// so a leaf that trusts this bundle trusts the same operator and resolves the
// same accounts. No seed is ever placed in the bundle.
func (id *identity) buildLeafBundle() (leafBundle, error) {
	oc, err := id.operatorClaims()
	if err != nil {
		return leafBundle{}, err
	}
	opJWT, err := oc.Encode(id.op)
	if err != nil {
		return leafBundle{}, fmt.Errorf("bus: leaf bundle: encode operator jwt: %w", err)
	}
	accJWT, err := id.accountJWT()
	if err != nil {
		return leafBundle{}, err
	}
	sysJWT, err := id.systemJWT()
	if err != nil {
		return leafBundle{}, err
	}
	return leafBundle{
		OperatorJWT: opJWT,
		AccountJWT:  accJWT,
		SystemJWT:   sysJWT,
		AccountKey:  pub(id.acc),
		SystemKey:   pub(id.sys),
	}, nil
}

// writeLeafArtifacts mints a SEXTANT-user link credential for the remote leaf and
// writes both the public trust bundle and the (secret) link credential into the
// store (ADR-0038). The link authenticates as a SEXTANT user — reusing the same
// per-client mint path the rest of the bus uses — so the leaf link is just
// another verified SEXTANT identity, not a privileged tier. Called once at Start
// when --leaf-listen is set; the bundle is world-readable trust material, the
// link credential is owner-only secret material.
func (b *Bus) writeLeafArtifacts() error {
	bundle, err := b.ident.buildLeafBundle()
	if err != nil {
		return fmt.Errorf("bus: leaf artifacts: %w", err)
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("bus: leaf artifacts: marshal bundle: %w", err)
	}
	if err := os.WriteFile(LeafBundlePath(b.store), data, 0o644); err != nil {
		return fmt.Errorf("bus: leaf artifacts: write bundle: %w", err)
	}
	// The link credential is a SEXTANT user scoped to the FEDERATION SET only
	// (leafLinkPermissions): pub+sub on sx.api.>, sx.deliver.>, _INBOX.>, sx.hb.> —
	// exactly what the link forwards, and nothing more. It is the transport, not a
	// client, but deliberately NOT an operator key: a credential that can carry
	// every agent's traffic must still not reach operator/admin/mint subjects.
	// Per-client scoping is enforced on each agent's OWN credential at the leaf
	// edge; the leaf still cannot mint (no account seed).
	linkJWT, linkSeed, _, err := b.ident.mintUser("sextant-leaf-link", leafLinkPermissions(), 0)
	if err != nil {
		return fmt.Errorf("bus: leaf artifacts: mint link credential: %w", err)
	}
	creds, err := credsFile(linkJWT, linkSeed)
	if err != nil {
		return fmt.Errorf("bus: leaf artifacts: format link credential: %w", err)
	}
	if err := writeOwnerOnly(LeafLinkCredsPath(b.store), creds); err != nil {
		return fmt.Errorf("bus: leaf artifacts: write link credential: %w", err)
	}
	return nil
}

// readLeafBundle loads and validates the hub's public trust bundle from path.
func readLeafBundle(path string) (leafBundle, error) {
	var lb leafBundle
	data, err := os.ReadFile(path)
	if err != nil {
		return lb, fmt.Errorf("bus: leaf: read bundle %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &lb); err != nil {
		return lb, fmt.Errorf("bus: leaf: parse bundle %s: %w", path, err)
	}
	if lb.OperatorJWT == "" || lb.AccountJWT == "" || lb.SystemJWT == "" || lb.AccountKey == "" || lb.SystemKey == "" {
		return lb, fmt.Errorf("bus: leaf: bundle %s is missing trust material", path)
	}
	return lb, nil
}

// leafServerAuthOptions installs the hub's PUBLIC trust bundle on a leaf's
// embedded-server options (ADR-0038): the hub operator as the trusted operator,
// the SEXTANT + system account JWTs in the resolver, and the system account. It
// is the public-only mirror of serverAuthOptions — same trust anchors, but
// sourced from encoded JWTs rather than local seeds, so the leaf holds no key
// material it could mint with. The leaf can authenticate the link and per-client
// credentials (all signed by the hub account) and enforce their perms; it cannot
// issue new ones.
func (lb leafBundle) leafServerAuthOptions(opts *natsserver.Options) error {
	oc, err := jwt.DecodeOperatorClaims(lb.OperatorJWT)
	if err != nil {
		return fmt.Errorf("bus: leaf: decode operator jwt: %w", err)
	}
	res := &natsserver.MemAccResolver{}
	if err := res.Store(lb.AccountKey, lb.AccountJWT); err != nil {
		return fmt.Errorf("bus: leaf: store account jwt: %w", err)
	}
	if err := res.Store(lb.SystemKey, lb.SystemJWT); err != nil {
		return fmt.Errorf("bus: leaf: store system jwt: %w", err)
	}
	opts.TrustedOperators = []*jwt.OperatorClaims{oc}
	opts.SystemAccount = lb.SystemKey
	opts.AccountResolver = res
	return nil
}

// leafRemoteOpts builds the RemoteLeafOpts that link this leaf to the hub
// (ADR-0038): the hub's nats-leaf:// URL, the SEXTANT-user link credential, and
// LocalAccount bound to the SEXTANT account so the federated subjects live in the
// one account the per-client perms are scoped to. Hub:true marks the remote as
// the hub side of the link.
func leafRemoteOpts(remoteURL, credsPath, accountKey string) (*natsserver.RemoteLeafOpts, error) {
	u, err := url.Parse(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("bus: leaf: parse remote url %q: %w", remoteURL, err)
	}
	if fi, err := os.Stat(credsPath); err != nil {
		return nil, fmt.Errorf("bus: leaf: link credential %s: %w", credsPath, err)
	} else if fi.IsDir() {
		return nil, fmt.Errorf("bus: leaf: link credential %s is a directory", credsPath)
	}
	return &natsserver.RemoteLeafOpts{
		URLs:         []*url.URL{u},
		Credentials:  credsPath,
		LocalAccount: accountKey,
		Hub:          true,
	}, nil
}

// validateLeafConfig checks the leaf-mode invariants before Start does anything
// expensive: leaf and hub-listen are mutually exclusive, and leaf mode needs both
// the bundle and the link credential. It fails loud and early (the fail-loud
// discipline) so a misconfigured leaf never half-starts.
func validateLeafConfig(cfg Config) error {
	if cfg.LeafRemoteURL != "" && cfg.LeafListenAddr != "" {
		return errors.New("bus: --leaf-remote and --leaf-listen are mutually exclusive (a bus is a leaf OR a hub, not both)")
	}
	if cfg.LeafRemoteURL == "" {
		return nil // hub mode (with or without a leaf listener)
	}
	if cfg.LeafBundle == "" {
		return errors.New("bus: leaf mode needs the hub's trust bundle (--leaf-bundle)")
	}
	if cfg.LeafCreds == "" {
		return errors.New("bus: leaf mode needs the hub-minted link credential (--leaf-creds)")
	}
	return nil
}

// applyHubLeafListener wires a hub-side leaf listener onto opts (ADR-0038) and
// fails CLOSED on an unsafe bind: it binds the configured host:port only when the
// host is loopback. A non-loopback (or all-interfaces) bind would be a routable
// unencrypted leaf listener — the one unacceptable configuration — and native
// leaf-listener TLS is not yet implemented, so the bus refuses it rather than
// open it. Loopback is allowed bare on purpose: it rides an external secure
// transport (SSH-R / Tailscale / WireGuard) that carries the encryption. Default-
// off — only called when LeafListenAddr is set.
func applyHubLeafListener(opts *natsserver.Options, addr string) error {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bus: --leaf-listen %q: %w", addr, err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("bus: --leaf-listen %q must bind a loopback host (127.0.0.1 / ::1): a non-loopback leaf listener would be routable and unencrypted (native leaf TLS is a follow-up); bind loopback behind a secure transport (SSH-R / Tailscale / WireGuard)", addr)
	}
	opts.LeafNode.Host = host
	opts.LeafNode.Port = port
	return nil
}

// isLoopbackHost reports whether host is a loopback address. An empty host (bind
// all interfaces) is NOT loopback — it is the routable case the leaf listener
// refuses. A non-IP host (a name) is treated as non-loopback: the bus will not
// guess, it fails closed.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// leafClientHost is the address a leaf's local client listener binds. Local
// agents on the remote box connect here; it is loopback-only on purpose — the
// leaf is reached over the federated link, never a routable client port.
const leafClientHost = "127.0.0.1"

// startLeaf launches the embedded bus in LEAF mode (ADR-0038): a JetStream-off
// server that links to a remote hub and federates the per-client wire-API
// subjects to it. It is deliberately thin compared to the hub's Start — there is
// no JetStream bootstrap, no operation serving, and no minting here, because the
// engine (and the sole minter) is the hub. The leaf's only jobs are to (a)
// install the hub's PUBLIC trust so it can authenticate the link and per-client
// credentials and enforce their perms locally, (b) solicit the leaf link to the
// hub, and (c) expose a loopback client listener for local agents. The caller
// must Shutdown it (the same Shutdown the hub uses; the nil-guarded fields are
// the leaf's no-ops).
func startLeaf(ctx context.Context, cfg Config) (*Bus, error) {
	logf := cfg.logf()
	bundle, err := readLeafBundle(cfg.LeafBundle)
	if err != nil {
		return nil, err
	}
	remote, err := leafRemoteOpts(cfg.LeafRemoteURL, cfg.LeafCreds, bundle.AccountKey)
	if err != nil {
		return nil, err
	}

	port := cfg.Port
	if port == 0 {
		port = -1 // a leaf's client port is ephemeral by default (no recorded-port reuse)
	}
	opts := &natsserver.Options{
		ServerName: "sextant-leaf",
		Host:       leafClientHost, // loopback only — the leaf is reached over the link, not a routable client port
		Port:       port,
		JetStream:  false, // the engine stays at the hub (ADR-0038 bright line)
		NoSigs:     true,  // the CLI owns signal handling
		// Mirror the hub's start: hold the client listener closed until the server
		// is ready, then open it explicitly with AcceptLoop. So a local agent can
		// never connect into a not-yet-ready leaf.
		DontListen: true,
	}
	if err := bundle.leafServerAuthOptions(opts); err != nil {
		return nil, err
	}
	opts.LeafNode.Remotes = []*natsserver.RemoteLeafOpts{remote}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("bus: leaf: new server: %w", err)
	}
	ns.Start()
	if err := waitReady(ctx, ns, 10*time.Second); err != nil {
		ns.Shutdown()
		return nil, err
	}

	// Ready: open the client listener (the hub's AcceptLoop pattern). Only now can a
	// local agent connect. The leaf link to the hub is solicited independently by
	// the server; WaitLeafLinked observes when it is up.
	ns.AcceptLoop(make(chan struct{}))
	if ns.Addr() == nil {
		ns.Shutdown()
		return nil, errors.New("bus: leaf: client listener failed to start")
	}

	freshness := cfg.HeartbeatFreshness
	if freshness <= 0 {
		freshness = defaultHeartbeatFreshness
	}
	b := &Bus{ns: ns, store: cfg.StoreDir, freshnessWindow: freshness, logf: logf}
	b.url = ns.ClientURL()
	logf("bus: leaf up — linking to hub %s (client listener %s)", cfg.LeafRemoteURL, b.url)
	return b, nil
}

// LeafLinked reports whether this leaf has an established link to its hub. It is
// best-effort presence for the leaf's own link (the hub-side view is the
// federated clients directory): a leaf with no link cannot reach the hub's wire
// API at all. A hub bus (no remotes) always reports false — it has no link to be.
func (b *Bus) LeafLinked() bool {
	lz, err := b.ns.Leafz(&natsserver.LeafzOptions{})
	if err != nil {
		return false
	}
	return lz.NumLeafs > 0
}

// WaitLeafLinked blocks until the leaf link is established or ctx is done. It is a
// convenience for callers (and tests) that must not race the link's async
// solicitation before federating a call. It returns ctx.Err() if the deadline
// passes first.
func (b *Bus) WaitLeafLinked(ctx context.Context) error {
	for {
		if b.LeafLinked() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// splitHostPort splits a host:port leaf address into its parts. An empty host is
// allowed (NATS binds all interfaces); the port must be a positive integer.
func splitHostPort(addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("port %q is not a positive integer", p)
	}
	return h, n, nil
}
