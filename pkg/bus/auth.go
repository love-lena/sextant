package bus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/love-lena/sextant/internal/wireapi"
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

// loadIdentity loads an existing identity, returning an error (never creating)
// when the store has no key material. Used by `sextant token`, so a wrong or
// empty --store fails clearly instead of minting a mismatched identity.
func loadIdentity(storeDir string) (*identity, error) {
	dir := keysDir(storeDir)
	op, err := loadSeed(filepath.Join(dir, "operator.nk"))
	if err != nil {
		return nil, identityErr(storeDir, err)
	}
	acc, err := loadSeed(filepath.Join(dir, "account.nk"))
	if err != nil {
		return nil, identityErr(storeDir, err)
	}
	sys, err := loadSeed(filepath.Join(dir, "system.nk"))
	if err != nil {
		return nil, identityErr(storeDir, err)
	}
	return &identity{op: op, acc: acc, sys: sys}, nil
}

func identityErr(storeDir string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("bus: no bus identity under %s — run `sextant up` there first", storeDir)
	}
	return err
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
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return false, err
	}
	if _, err := f.Write(seed); err != nil {
		f.Close()
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
	ac.Limits.JetStreamLimits.DiskStorage = -1
	ac.Limits.JetStreamLimits.MemoryStorage = -1
	ac.Limits.JetStreamLimits.Streams = -1
	ac.Limits.JetStreamLimits.Consumer = -1
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
func (id *identity) mintUser(name string, perms jwt.Permissions, tags ...string) (userJWT, seed, subject string, err error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return "", "", "", fmt.Errorf("bus: create user key: %w", err)
	}
	subject = pub(ukp)
	uc := jwt.NewUserClaims(subject)
	uc.Name = name
	uc.IssuerAccount = pub(id.acc)
	uc.Permissions = perms
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
	defer f.Close()
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
	var p jwt.Permissions
	// Publish: only this client's own Wire API call space (sx.api.<id>.>). The
	// <id> in the subject is the authenticated identity, so the author the bus
	// stamps from it cannot be forged.
	p.Pub.Allow = []string{wireapi.APIPrefix + clientID + ".>"}
	// Subscribe: this client's own push-delivery space (subscribe/watch/drain
	// deliveries: sx.deliver.<id>.>) and the request/reply inbox. _INBOX.> is
	// mandatory — a client's own nc.Request receives the bus's reply there, so
	// without it every call would time out. (allow_responses governs only a
	// responder replying outward; it does not let a requester receive a reply.)
	p.Sub.Allow = []string{
		wireapi.DeliverPrefix + clientID + ".>",
		"_INBOX.>",
	}
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

// MintClientToken mints a client-tier credentials file for a client whose
// human display_name is given, loading the account signing key from the store.
// The bus mints the client's primary id — a ULID — so every client identity is
// bus-owned and unforgeable (ADR-0019); the display_name is carried as a JWT tag.
// It returns the creds file and the minted ULID id. This is what `sextant token`
// calls; it requires an existing identity (it never creates one) so a wrong
// --store fails clearly rather than minting a mismatched credential.
func MintClientToken(storeDir, displayName string) (creds, id string, err error) {
	if err := validateDisplayName(displayName); err != nil {
		return "", "", err
	}
	ident, err := loadIdentity(storeDir)
	if err != nil {
		return "", "", err
	}
	id = ulid.Make().String()
	j, seed, subject, err := ident.mintUser(id, clientPermissions(id), wireapi.EncodeDisplayNameTag(displayName))
	if err != nil {
		return "", "", err
	}
	// Record the issued id (and the authenticated subject) for audit. The id is a
	// fresh ULID, so this never collides; the ledger is a durable issuance trail.
	if err := reserveName(storeDir, id, subject); err != nil {
		return "", "", err
	}
	c, err := credsFile(j, seed)
	if err != nil {
		return "", "", err
	}
	return c, id, nil
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
