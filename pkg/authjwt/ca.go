package authjwt

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrCAKeyMissing is returned when one of the CA files does not exist.
var ErrCAKeyMissing = errors.New("authjwt: CA key file missing")

// ErrInvalidToken wraps every verification failure (bad signature, bad
// claims, expired). Callers should treat any error as "reject".
var ErrInvalidToken = errors.New("authjwt: invalid token")

// PEM block types we recognize.
const (
	pemBlockPrivate = "ED25519 PRIVATE KEY"
	pemBlockPublic  = "ED25519 PUBLIC KEY"
)

// CA wraps the sextant signing keypair. Construct via LoadCA or
// GenerateCA + LoadCA. A zero CA is unusable.
type CA struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// GenerateCA returns a fresh ED25519 keypair PEM-encoded as PKCS8 (for
// the private key) and PKIX (for the public key). Callers persist the
// returned bytes to disk; LoadCA reads them back.
//
// The returned PEM blocks use sextant-specific type names so accidental
// confusion with TLS keys is unlikely.
func GenerateCA() (privPEM, pubPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("authjwt: generate ed25519 keypair: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("authjwt: marshal private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, nil, fmt.Errorf("authjwt: marshal public key: %w", err)
	}
	privPEM = pem.EncodeToMemory(&pem.Block{Type: pemBlockPrivate, Bytes: privDER})
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: pemBlockPublic, Bytes: pubDER})
	return privPEM, pubPEM, nil
}

// LoadCA reads PEM-encoded files from privPath and pubPath, parses them,
// and asserts they form a matching ED25519 keypair.
func LoadCA(privPath, pubPath string) (*CA, error) {
	privRaw, err := os.ReadFile(privPath) //nolint:gosec // path is operator-controlled
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrCAKeyMissing, privPath)
		}
		return nil, fmt.Errorf("authjwt: read private key %s: %w", privPath, err)
	}
	pubRaw, err := os.ReadFile(pubPath) //nolint:gosec // path is operator-controlled
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrCAKeyMissing, pubPath)
		}
		return nil, fmt.Errorf("authjwt: read public key %s: %w", pubPath, err)
	}
	priv, err := parsePrivatePEM(privRaw)
	if err != nil {
		return nil, fmt.Errorf("authjwt: parse %s: %w", privPath, err)
	}
	pub, err := parsePublicPEM(pubRaw)
	if err != nil {
		return nil, fmt.Errorf("authjwt: parse %s: %w", pubPath, err)
	}
	derived, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("authjwt: private key did not yield ed25519 public key")
	}
	if !derived.Equal(pub) {
		return nil, fmt.Errorf("authjwt: CA pub/priv do not match")
	}
	return &CA{priv: priv, pub: pub}, nil
}

// PublicKey returns the public half. Useful for components (NATS, MCP)
// that verify tokens without needing the private key.
func (c *CA) PublicKey() ed25519.PublicKey {
	if c == nil {
		return nil
	}
	return c.pub
}

// Issue signs a JWT carrying claims. Claims.IssuedAt and Claims.ExpiresAt
// must be set; Issue does not assume defaults. The returned token is
// compact-serialized JWT (`xxx.yyy.zzz`).
func (c *CA) Issue(claims Claims) (string, error) {
	if c == nil || c.priv == nil {
		return "", fmt.Errorf("authjwt: nil CA")
	}
	if err := claims.validate(); err != nil {
		return "", err
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims.toRegistered())
	signed, err := tok.SignedString(c.priv)
	if err != nil {
		return "", fmt.Errorf("authjwt: sign token: %w", err)
	}
	return signed, nil
}

// Verify parses a token, checks its signature against the CA public key,
// asserts the algorithm is EdDSA, and confirms ExpiresAt > now. Any
// failure returns an error wrapping ErrInvalidToken.
func (c *CA) Verify(token string) (Claims, error) {
	if c == nil || c.pub == nil {
		return Claims{}, fmt.Errorf("authjwt: nil CA")
	}
	var rc registeredClaims
	parsed, err := jwt.ParseWithClaims(token, &rc, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodEdDSA.Alg() {
			return nil, fmt.Errorf("unexpected signing method %s", t.Method.Alg())
		}
		return c.pub, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}))
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if !parsed.Valid {
		return Claims{}, fmt.Errorf("%w: token reports invalid", ErrInvalidToken)
	}
	out, err := rc.toClaims()
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return out, nil
}

// parsePrivatePEM accepts a PEM block of type ED25519 PRIVATE KEY or the
// stdlib's generic "PRIVATE KEY" (PKCS8). Both forms decode the same
// payload; we accept either for tooling flexibility.
func parsePrivatePEM(raw []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	switch block.Type {
	case pemBlockPrivate, "PRIVATE KEY":
	default:
		return nil, fmt.Errorf("unexpected PEM type %q", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ed25519.PrivateKey", key)
	}
	return priv, nil
}

func parsePublicPEM(raw []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	switch block.Type {
	case pemBlockPublic, "PUBLIC KEY":
	default:
		return nil, fmt.Errorf("unexpected PEM type %q", block.Type)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ed25519.PublicKey", key)
	}
	return pub, nil
}

// Now is exposed for tests that need to pin the verification clock. The
// default is time.Now; tests reassign and restore.
var Now = time.Now
