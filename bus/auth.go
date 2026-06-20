package bus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
	"github.com/oklog/ulid/v2"
)

// The bus authenticates with NATS decentralized JWT auth (ADR-0012): a single
// operator, one SEXTANT account (where all clients live and the sx_ infra is
// provisioned), a system account, and one user JWT per client — so every
// connection is a distinct, verified identity and every op is attributable.
// Each client's credential carries a per-client allow-list (ADR-0019): it may
// publish only under its own call prefix and subscribe only to its own delivery
// space, which is what makes the bus-stamped author unforgeable.

// identity is the bus's signing material: the operator and account key pairs,
// persisted in the store so `sextant token` can mint user JWTs out-of-band.
type identity struct {
	op  nkeys.KeyPair // operator (root of trust)
	acc nkeys.KeyPair // SEXTANT account (signs user JWTs)
	sys nkeys.KeyPair // system account
}

func keysDir(storeDir string) string { return filepath.Join(storeDir, "keys") }

// loadOrCreateIdentity loads the persisted operator/account/system seeds from
// the store, creating and persisting them on first run. Used by the bus.
func loadOrCreateIdentity(storeDir string) (*identity, error) {
	dir, err := ensureKeysDir(storeDir)
	if err != nil {
		return nil, err
	}
	op, err := loadOrCreateSeed(filepath.Join(dir, "operator.nk"), nkeys.CreateOperator)
	if err != nil {
		return nil, err
	}
	acc, err := loadOrCreateSeed(filepath.Join(dir, "account.nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	sys, err := loadOrCreateSeed(filepath.Join(dir, "system.nk"), nkeys.CreateAccount)
	if err != nil {
		return nil, err
	}
	return &identity{op: op, acc: acc, sys: sys}, nil
}

// ensureKeysDir creates the keys dir (0700) and verifies it isn't accessible to
// other users.
func ensureKeysDir(storeDir string) (string, error) {
	dir := keysDir(storeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("bus: keys dir: %w", err)
	}
	if fi, err := os.Lstat(dir); err != nil {
		return "", err
	} else if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("bus: keys dir %s has insecure mode %o (want 0700)", dir, fi.Mode().Perm())
	}
	return dir, nil
}

// loadSeed reads and parses a seed file after verifying it is a non-symlinked,
// owner-only regular file. A missing file surfaces fs.ErrNotExist.
func loadSeed(path string) (nkeys.KeyPair, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("bus: refusing to load symlinked seed %s", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("bus: seed %s has insecure mode %o (want 0600)", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kp, err := nkeys.FromSeed(b)
	if err != nil {
		return nil, fmt.Errorf("bus: parse seed %s: %w", path, err)
	}
	return kp, nil
}

// loadOrCreateSeed loads an existing seed (verifying perms) or atomically
// creates one. The atomic create — write a temp file, then hard-link it into
// place — tolerates a concurrent first run: the loser reuses the winner's seed.
func loadOrCreateSeed(path string, create func() (nkeys.KeyPair, error)) (nkeys.KeyPair, error) {
	kp, err := loadSeed(path)
	if err == nil {
		return kp, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	kp, err = create()
	if err != nil {
		return nil, fmt.Errorf("bus: create key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, err
	}
	raced, err := writeNewSeed(path, seed)
	if err != nil {
		return nil, err
	}
	if raced {
		return loadSeed(path) // another process won the race; use its seed
	}
	return kp, nil
}

// writeNewSeed writes seed to path atomically, only if path does not already
// exist. It reports raced=true (and no error) when another writer got there
// first.
func writeNewSeed(path string, seed []byte) (raced bool, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return false, fmt.Errorf("bus: temp seed: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return false, err
	}
	if _, err := f.Write(seed); err != nil {
		_ = f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return true, nil
		}
		return false, fmt.Errorf("bus: persist seed %s: %w", path, err)
	}
	return false, nil
}

func pub(kp nkeys.KeyPair) string {
	p, err := kp.PublicKey()
	if err != nil {
		// A constructed/loaded keypair always has a public key; a failure here
		// is an unrecoverable invariant violation on the JWT trust path —
		// fail loud rather than emit an empty subject into security claims.
		panic(fmt.Errorf("bus: keypair has no public key: %w", err))
	}
	return p
}

// operatorClaims builds the (self-signed) operator claims naming the system
// account, for the server's TrustedOperators.
func (id *identity) operatorClaims() (*jwt.OperatorClaims, error) {
	oc := jwt.NewOperatorClaims(pub(id.op))
	oc.Name = "sextant"
	oc.SystemAccount = pub(id.sys)
	encoded, err := oc.Encode(id.op)
	if err != nil {
		return nil, fmt.Errorf("bus: encode operator jwt: %w", err)
	}
	return jwt.DecodeOperatorClaims(encoded)
}

// accountJWT builds the SEXTANT account JWT (JetStream enabled), signed by the
// operator.
func (id *identity) accountJWT() (string, error) {
	ac := jwt.NewAccountClaims(pub(id.acc))
	ac.Name = "SEXTANT"
	ac.Limits.DiskStorage = -1
	ac.Limits.MemoryStorage = -1
	ac.Limits.Streams = -1
	ac.Limits.Consumer = -1
	return ac.Encode(id.op)
}

// systemJWT builds the system account JWT, signed by the operator.
func (id *identity) systemJWT() (string, error) {
	sc := jwt.NewAccountClaims(pub(id.sys))
	sc.Name = "SYS"
	return sc.Encode(id.op)
}

// mintUser signs a user JWT in the SEXTANT account with the given name,
// permissions, and tags, returning the JWT, the user's seed, and the user's
// subject (its public key — the principal the bus actually authenticates).
//
// ttl bounds the credential's lifetime (ADR-0044): when ttl > 0 the JWT carries a
// standard `exp` claim ttl from now, which the NATS server enforces (an expired
// credential is rejected on connect/reconnect). ttl == 0 is the perpetual case —
// no `exp` is set, byte-identical to before — which is what every infrastructure
// and ordinary-client mint passes; only a short-lived browser credential sets it.
func (id *identity) mintUser(name string, perms jwt.Permissions, ttl time.Duration, tags ...string) (userJWT, seed, subject string, err error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return "", "", "", fmt.Errorf("bus: create user key: %w", err)
	}
	subject = pub(ukp)
	uc := jwt.NewUserClaims(subject)
	uc.Name = name
	uc.IssuerAccount = pub(id.acc)
	uc.Permissions = perms
	if ttl > 0 {
		uc.Expires = time.Now().Add(ttl).Unix()
	}
	uc.Tags.Add(tags...)
	j, err := uc.Encode(id.acc)
	if err != nil {
		return "", "", "", fmt.Errorf("bus: encode user jwt: %w", err)
	}
	sb, err := ukp.Seed()
	if err != nil {
		return "", "", "", err
	}
	return j, string(sb), subject, nil
}

// validateDisplayName rejects a human display_name that isn't safe to carry in a
// JWT tag and a registry record. Since a client's primary id is now a bus-minted
// ULID (not the display_name), the display_name need not be a key or filename —
// it is just a readable label — so this is permissive: non-empty, bounded, and
// free of control characters (which would corrupt the tag/JSON).
func validateDisplayName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("bus: display name is empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("bus: display name %q is too long (max 128)", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("bus: display name %q contains a control character", name)
		}
	}
	return nil
}

// reserveName atomically claims id in the issued-names ledger under the store,
// recording the authenticated subject for audit. It fails loud if id was
// already issued — a name collision must surface rather than silently mint a
// second identity that shares a registry key (the "ghost client" footgun). The
// O_EXCL create is the atomic guard, safe under concurrent minting.
func reserveName(storeDir, id, subject string) error {
	dir := filepath.Join(storeDir, "issued")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("bus: issued-names dir: %w", err)
	}
	path := filepath.Join(dir, id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("bus: client id %q already has credentials (delete %s to reissue)", id, path)
		}
		return fmt.Errorf("bus: reserve client id %q: %w", id, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(subject + "\n"); err != nil {
		return fmt.Errorf("bus: record issued id %q: %w", id, err)
	}
	return nil
}

// operatorPermissions is full access — the bus's own connection and tooling.
func operatorPermissions() jwt.Permissions { return jwt.Permissions{} }

// clientPermissions is the ADR-0019 client-tier guardrail: a per-client JWT
// allow-list, scoped to clientID (the bus-minted ULID that is this credential's
// authenticated identity). It is the keystone of the unforgeable author — a
// client may publish ONLY under its own call prefix, so the subject token the
// bus reads the author from is exactly the identity NATS authenticated, and no
// client can issue a call (and thus stamp a frame) under another id.
//
// Allow-list, not deny-list: everything is denied unless named. A client
// reaches the messages stream, the KV buckets, and the control space only by
// asking the bus over a call — never directly — so there is no stream or bucket
// lifecycle to squat and no operator state to read.
func clientPermissions(clientID string) jwt.Permissions {
	// The id is woven into NATS subject patterns below, so it must be a single
	// plain subject token. A bus-minted ULID always is; this guards the security
	// path against a future custom-id path slipping in a `.` (which would misparse
	// the call subject) or a wildcard (`*`/`>`, which would broaden the allow-list
	// past the client's own scope). Fail loud — a malformed id here is a
	// programming error on the trust path, not user input.
	if strings.ContainsAny(clientID, ".*> \t\r\n") || clientID == "" {
		panic(fmt.Sprintf("bus: client id %q is not a single subject token (allow-list scoping is unsafe)", clientID))
	}
	var p jwt.Permissions
	// Publish: only this client's own Wire API call space (sx.api.<id>.>). The
	// <id> in the subject is the authenticated identity, so the author the bus
	// stamps from it cannot be forged.
	p.Pub.Allow = []string{wireapi.APIPrefix + clientID + ".>"}
	// Subscribe: this client's own push-delivery space (subscribe/watch/drain
	// deliveries: sx.deliver.<id>.>) and its OWN request/reply inbox
	// (_INBOX.<id>.>). The inbox must be subscribable or a client's own nc.Request
	// never receives the bus's reply and every call times out. It is scoped per
	// client, not the shared _INBOX.> — otherwise any client could subscribe the
	// wildcard and eavesdrop on every other client's call replies. The SDK sets a
	// matching nats.CustomInboxPrefix so its replies land under this prefix.
	// (allow_responses/Resp is not needed: a client is a requester, never a
	// responder.)
	p.Sub.Allow = []string{
		wireapi.DeliverPrefix + clientID + ".>",
		wireapi.InboxPrefix(clientID) + ".>",
		// This client's own heartbeat-echo subject (sx.hb.<id>, TASK-126): the
		// echo watcher subscribes it to confirm its push path is live. Scoped to
		// the client's own id like the delivery space, so no client can read
		// another's beat. Additive — a credential minted before this lands omits
		// the entry, and the echo watcher then simply receives nothing (graceful).
		wireapi.HeartbeatSubject(clientID),
	}
	return p
}

// leafLinkPermissions is the leaf link's grant (ADR-0038): the federation set,
// minus the reserved issuance identities. The remote leaf authenticates the link
// to the hub as a SEXTANT user carrying this credential, and the link forwards the
// per-client wire-API subjects across it — so the link needs pub+sub on those
// subjects, wildcarded across all ids because it carries traffic for every agent
// behind the leaf. It is deliberately NOT operatorPermissions(): possession of
// the link credential must not be an operator key.
//
// The federation wildcards (sx.api.>, _INBOX.>, sx.deliver.>) would otherwise also
// cover the reserved operator/enroll subjects. So the grant DENIES those reserved
// prefixes (deny-wins in NATS): the link can forward any ordinary agent's traffic
// but can never itself ask the bus to mint/retire/claim, nor reach operator/enroll
// state. Per-client SCOPING for honest agents is still enforced on each agent's OWN
// credential at the leaf's edge — not on the link.
//
// The deny is SYMMETRIC across pub and sub and covers the WHOLE reserved surface —
// the call space (`sx.api.operator/enroll.>`), the reply inbox
// (`_INBOX.operator/enroll.>`), and the push-delivery space
// (`sx.deliver.operator/enroll.>`). The operator/enroll identities are hub-local —
// no client ever connects to them over a leaf — so the link has no legitimate
// reason to touch their subjects, and denying all of them closes three distinct
// exposures with one consistent rule: it cannot make an issuance call, cannot
// intercept an issuance reply's freshly-minted credential, and cannot eavesdrop
// the operator's push streams (principal.watch / artifact.watch deliveries). What
// remains — by necessity, not oversight — is access to NORMAL federated clients'
// inbox/delivery subjects, which the link must carry; see ADR-0038's trust
// boundary.
func leafLinkPermissions() jwt.Permissions {
	var p jwt.Permissions
	fed := []string{
		wireapi.APIPrefix + ">",       // sx.api.<id>.>  — per-client calls
		wireapi.DeliverPrefix + ">",   // sx.deliver.<id>.> — push delivery
		"_INBOX.>",                    // _INBOX.<id>.> — call replies
		wireapi.HeartbeatPrefix + ">", // sx.hb.<id> — heartbeat echo
	}
	// The whole operator/enroll surface, carved out of the federation wildcards.
	// Applied to BOTH pub and sub: the link touches no reserved subject in either
	// direction. (Heartbeat-echo is omitted — operator/enroll do not heartbeat, so
	// there is no sx.hb.operator/enroll to deny.)
	var denyReserved []string
	for _, id := range []string{wireapi.OperatorID, wireapi.EnrollID} {
		denyReserved = append(
			denyReserved,
			wireapi.APIPrefix+id+".>",     // sx.api.<reserved>.>     — issuance/retire/claim calls
			wireapi.InboxPrefix(id)+".>",  // _INBOX.<reserved>.>     — issuance reply (minted creds)
			wireapi.DeliverPrefix+id+".>", // sx.deliver.<reserved>.> — operator push streams
		)
	}
	// Both directions: the link forwards calls TO the hub and carries
	// deliveries/replies/echoes BACK to the leaf, so the federation set is allowed
	// on pub and sub alike. The author the hub stamps still comes from the call
	// subject, which each agent's own credential already scoped at the leaf edge.
	p.Pub.Allow = append([]string(nil), fed...)
	p.Pub.Deny = append([]string(nil), denyReserved...)
	p.Sub.Allow = append([]string(nil), fed...)
	p.Sub.Deny = append([]string(nil), denyReserved...)
	return p
}

// credsFile formats a user JWT + seed as a NATS credentials file.
func credsFile(userJWT, seed string) (string, error) {
	b, err := jwt.FormatUserConfig(userJWT, []byte(seed))
	if err != nil {
		return "", fmt.Errorf("bus: format creds: %w", err)
	}
	return string(b), nil
}

// mintIdentity mints a new client credential — a fresh ULID id and its per-client
// allow-list credential — using the bus's own signing keys (b.ident). The keys
// never leave the bus, so the bus is the sole minter (key custody, ADR-0020). It
// records the issued id + authenticated subject in the durable ledger and returns
// the creds text, the id, and the subject (the authenticated public key, which
// presence joins against the live connection table). It does NOT persist the
// registry record — MintClient does, so issuance is one atomic act.
//
// ttl bounds the credential's JWT lifetime (ADR-0044): 0 is perpetual (the
// ordinary case, unchanged), a positive value sets a JWT `exp` for a short-lived
// credential the issuer cannot retire (a browser child).
func (b *Bus) mintIdentity(displayName string, ttl time.Duration) (creds, id, subject string, err error) {
	if err := validateDisplayName(displayName); err != nil {
		return "", "", "", err
	}
	id = ulid.Make().String()
	j, seed, subject, err := b.ident.mintUser(id, clientPermissions(id), ttl, wireapi.EncodeDisplayNameTag(displayName))
	if err != nil {
		return "", "", "", err
	}
	// Record the issued id (and the authenticated subject) for audit. The id is a
	// fresh ULID, so this never collides; the ledger is a durable issuance trail.
	if err := reserveName(b.store, id, subject); err != nil {
		return "", "", "", err
	}
	c, err := credsFile(j, seed)
	if err != nil {
		return "", "", "", err
	}
	return c, id, subject, nil
}

// OperatorCredsPath and EnrollCredsPath are the two reserved infra-credential
// files `sextant up` provisions in the store (ADR-0020). They are minted
// credentials, not signing keys — the keys stay in the bus. Locality is the trust:
// a process that can read these files is on the operator's machine.
func OperatorCredsPath(storeDir string) string { return filepath.Join(storeDir, "operator.creds") }
func EnrollCredsPath(storeDir string) string   { return filepath.Join(storeDir, "enroll.creds") }

// operatorCredPermissions is the held-identity authority (ADR-0020): the operator
// may call any Wire API op under its own reserved prefix — issuance
// (clients.register), retire, clients.list — and receive replies on its own inbox.
func operatorCredPermissions() jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow = []string{wireapi.APIPrefix + wireapi.OperatorID + ".>"}
	p.Sub.Allow = []string{
		wireapi.DeliverPrefix + wireapi.OperatorID + ".>",
		wireapi.InboxPrefix(wireapi.OperatorID) + ".>",
	}
	return p
}

// enrollCredPermissions is the bootstrap/enrollment authority (ADR-0020, ADR-0031):
// a deliberately narrow allow-list. The enrollment identity may call exactly two
// operations and receive their replies — clients.register (to self-enroll) and
// principal.set (to claim the still-unclaimed principal as the first human seat
// self-enrolls, ADR-0031). The bus gates the latter to a claim: enroll can point
// an unclaimed principal at a non-agent seat, never re-point an established one.
// It still cannot publish messages, retire, or read the directory. This is the
// enrollment connection tier: how an identity-less local process reaches the
// bootstrap path at all.
func enrollCredPermissions() jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow = []string{
		wireapi.CallSubject(wireapi.EnrollID, wireapi.OpClientsRegister),
		wireapi.CallSubject(wireapi.EnrollID, wireapi.OpPrincipalSet),
	}
	p.Sub.Allow = []string{wireapi.InboxPrefix(wireapi.EnrollID) + ".>"}
	return p
}

// provisionInfraCreds mints the operator and enrollment credentials and writes
// them into the store (ADR-0020). Called once at Start; idempotent in effect (it
// overwrites with a freshly minted credential each boot — the identities are
// reserved names, not durable records). Both files are owner-only.
func (b *Bus) provisionInfraCreds() error {
	infra := []struct {
		id    string
		perms jwt.Permissions
		path  string
	}{
		{wireapi.OperatorID, operatorCredPermissions(), OperatorCredsPath(b.store)},
		{wireapi.EnrollID, enrollCredPermissions(), EnrollCredsPath(b.store)},
	}
	for _, in := range infra {
		// ttl=0: the infra credentials are perpetual (re-minted fresh each boot),
		// the unchanged behaviour — only a browser child gets a bounded TTL.
		j, seed, _, err := b.ident.mintUser(in.id, in.perms, 0)
		if err != nil {
			return fmt.Errorf("bus: provision %s credential: %w", in.id, err)
		}
		c, err := credsFile(j, seed)
		if err != nil {
			return fmt.Errorf("bus: provision %s credential: %w", in.id, err)
		}
		if err := writeOwnerOnly(in.path, c); err != nil {
			return fmt.Errorf("bus: write %s credential %s: %w", in.id, in.path, err)
		}
	}
	return nil
}

// writeOwnerOnly writes content to path as a fresh owner-only (0600) file,
// replacing any existing file atomically (temp file + rename). Unlike
// os.WriteFile, it guarantees 0600 even when path already exists with looser
// permissions — a reused or user-supplied store can hold a world-readable
// leftover, and these credentials authorize identity issuance and retirement.
func writeOwnerOnly(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".creds-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil { // defensive against umask
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// serverAuthOptions sets the JWT auth fields on opts and returns the in-memory
// account resolver (preloaded with the SEXTANT and system account JWTs).
func (id *identity) serverAuthOptions(opts *natsserver.Options) error {
	oc, err := id.operatorClaims()
	if err != nil {
		return err
	}
	accJWT, err := id.accountJWT()
	if err != nil {
		return err
	}
	sysJWT, err := id.systemJWT()
	if err != nil {
		return err
	}
	res := &natsserver.MemAccResolver{}
	if err := res.Store(pub(id.acc), accJWT); err != nil {
		return fmt.Errorf("bus: store account jwt: %w", err)
	}
	if err := res.Store(pub(id.sys), sysJWT); err != nil {
		return fmt.Errorf("bus: store system jwt: %w", err)
	}
	opts.TrustedOperators = []*jwt.OperatorClaims{oc}
	opts.SystemAccount = pub(id.sys)
	opts.AccountResolver = res
	return nil
}
