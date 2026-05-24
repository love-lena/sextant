package authjwt

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the sextant-specific JWT payload. The MCP server reads
// Capabilities to authorize tool calls; NATS reads Subject (the agent
// UUID, as a string) to identify the publisher. AgentUUID and
// IncarnationID are duplicated into typed fields for explicit access.
type Claims struct {
	// AgentUUID identifies the agent (permanent identity).
	AgentUUID uuid.UUID

	// IncarnationID identifies the specific incarnation. Pairs with the
	// per-incarnation lifetime so revocation = kill the incarnation.
	IncarnationID uuid.UUID

	// Capabilities lists the MCP tool / capability names this incarnation
	// is allowed to invoke. Verified by the MCP server (M10); NATS subject
	// ACLs are encoded server-side, not here.
	Capabilities []string

	// IssuedAt is the time the token was minted. Must be non-zero.
	IssuedAt time.Time

	// ExpiresAt is the incarnation lifetime ceiling. Must be after
	// IssuedAt.
	ExpiresAt time.Time

	// Issuer is a sextant install identifier (typically "sextantd@<host>").
	// Verifiers may compare against an expected value but the library
	// does not enforce a particular form.
	Issuer string
}

func (c Claims) validate() error {
	if c.AgentUUID == uuid.Nil {
		return fmt.Errorf("authjwt: claims missing AgentUUID")
	}
	if c.IncarnationID == uuid.Nil {
		return fmt.Errorf("authjwt: claims missing IncarnationID")
	}
	if c.IssuedAt.IsZero() {
		return fmt.Errorf("authjwt: claims missing IssuedAt")
	}
	if c.ExpiresAt.IsZero() {
		return fmt.Errorf("authjwt: claims missing ExpiresAt")
	}
	if !c.ExpiresAt.After(c.IssuedAt) {
		return fmt.Errorf("authjwt: ExpiresAt must be after IssuedAt")
	}
	return nil
}

// registeredClaims is the wire shape of a sextant JWT. Embedding
// jwt.RegisteredClaims gives us standard "exp", "iat", "iss", "sub"
// fields for free; the sextant-specific fields use the "sxt_" prefix to
// keep them recognizable in raw logs.
type registeredClaims struct {
	jwt.RegisteredClaims
	IncarnationID string   `json:"sxt_inc"`
	Capabilities  []string `json:"sxt_caps"`
}

func (c Claims) toRegistered() registeredClaims {
	caps := append([]string(nil), c.Capabilities...)
	return registeredClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.Issuer,
			Subject:   c.AgentUUID.String(),
			IssuedAt:  jwt.NewNumericDate(c.IssuedAt),
			ExpiresAt: jwt.NewNumericDate(c.ExpiresAt),
		},
		IncarnationID: c.IncarnationID.String(),
		Capabilities:  caps,
	}
}

func (r registeredClaims) toClaims() (Claims, error) {
	agentUUID, err := uuid.Parse(r.Subject)
	if err != nil {
		return Claims{}, fmt.Errorf("parse sub uuid %q: %w", r.Subject, err)
	}
	incID, err := uuid.Parse(r.IncarnationID)
	if err != nil {
		return Claims{}, fmt.Errorf("parse sxt_inc uuid %q: %w", r.IncarnationID, err)
	}
	out := Claims{
		AgentUUID:     agentUUID,
		IncarnationID: incID,
		Capabilities:  append([]string(nil), r.Capabilities...),
		Issuer:        r.Issuer,
	}
	if r.IssuedAt != nil {
		out.IssuedAt = r.IssuedAt.Time
	}
	if r.ExpiresAt != nil {
		out.ExpiresAt = r.ExpiresAt.Time
	}
	return out, nil
}
