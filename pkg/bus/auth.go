package bus

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
)

// The bus authenticates with NATS decentralized JWT auth (ADR-0012): a single
// operator, one SEXTANT account (where all clients live and the sx_ infra is
// provisioned), a system account, and one user JWT per client — so every
// connection is a distinct, verified identity and every op is attributable.
// The same client-tier permission template applies to all clients for now;
// per-client (write-precision) permissions are the deferred refinement.

// identity is the bus's signing material: the operator and account key pairs,
// persisted in the store so `sextant token` can mint user JWTs out-of-band.
type identity struct {
	op  nkeys.KeyPair // operator (root of trust)
	acc nkeys.KeyPair // SEXTANT account (signs user JWTs)
	sys nkeys.KeyPair // system account
}

func keysDir(storeDir string) string { return filepath.Join(storeDir, "keys") }

// loadOrCreateIdentity loads the persisted operator/account/system seeds from
// the store, creating and persisting them on first run.
func loadOrCreateIdentity(storeDir string) (*identity, error) {
	dir := keysDir(storeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("bus: keys dir: %w", err)
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

func loadOrCreateSeed(path string, create func() (nkeys.KeyPair, error)) (nkeys.KeyPair, error) {
	if b, err := os.ReadFile(path); err == nil {
		kp, err := nkeys.FromSeed(b)
		if err != nil {
			return nil, fmt.Errorf("bus: parse seed %s: %w", path, err)
		}
		return kp, nil
	}
	kp, err := create()
	if err != nil {
		return nil, fmt.Errorf("bus: create key: %w", err)
	}
	seed, err := kp.Seed()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		return nil, fmt.Errorf("bus: persist seed %s: %w", path, err)
	}
	return kp, nil
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

// mintUser signs a user JWT in the SEXTANT account with the given name and
// permissions, returning the JWT and the user's seed.
func (id *identity) mintUser(name string, perms jwt.Permissions) (userJWT, seed string, err error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return "", "", fmt.Errorf("bus: create user key: %w", err)
	}
	uc := jwt.NewUserClaims(pub(ukp))
	uc.Name = name
	uc.IssuerAccount = pub(id.acc)
	uc.Permissions = perms
	j, err := uc.Encode(id.acc)
	if err != nil {
		return "", "", fmt.Errorf("bus: encode user jwt: %w", err)
	}
	sb, err := ukp.Seed()
	if err != nil {
		return "", "", err
	}
	return j, string(sb), nil
}

// operatorPermissions is full access — the bus's own connection and tooling.
func operatorPermissions() jwt.Permissions { return jwt.Permissions{} }

// clientPermissions is the ADR-0012 client-tier guardrail, as JWT permissions.
func clientPermissions() jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Deny = []string{
		sx.ControlPrefix + ">",          // operator-only control
		"$KV." + sx.BucketSystem + ".>", // no system writes
		// No stream/bucket lifecycle (the operator provisions buckets).
		"$JS.API.STREAM.CREATE.>",
		"$JS.API.STREAM.UPDATE.>",
		"$JS.API.STREAM.DELETE.>",
		"$JS.API.STREAM.PURGE.>",
	}
	p.Sub.Deny = []string{
		"$KV." + sx.BucketSystem + ".>", // no system reads via consumer
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

// MintClientToken mints a client-tier credentials file for id, loading the
// account signing key from the store. This is what `sextant token` calls.
func MintClientToken(storeDir, id string) (string, error) {
	ident, err := loadOrCreateIdentity(storeDir)
	if err != nil {
		return "", err
	}
	j, seed, err := ident.mintUser(id, clientPermissions())
	if err != nil {
		return "", err
	}
	return credsFile(j, seed)
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
