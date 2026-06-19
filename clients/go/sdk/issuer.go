package sextant

import (
	"context"
	"errors"
	"fmt"

	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/nats-io/nats.go"
)

// Issuer is a connection to the bus used to issue and retire identities (ADR-0020)
// — the held-identity (operator) or bootstrap/enrollment authority. It is NOT a
// full client: it runs no connect handshake (no clients.hello, no drain watch) and
// holds no durable identity record of its own, because its job is to mint and
// decommission OTHER identities, not to participate as a collaborating client.
//
// The CLI drives it with the credentials `sextant up` provisions in the store:
// the operator credential authorizes `clients register <name>` (mint for another)
// and `clients retire <id>`; the enrollment credential authorizes `clients
// register --self` (mint for self). The bus enforces which credential may do what
// — the Issuer just carries the call.
type Issuer struct {
	nc *nats.Conn
	id string // the reserved id this connection authenticates as (operator/enroll)
}

// IssuedClient is the result of registering a new identity: its bus-minted ULID id
// and its credential (JWT+seed) text, to be written to a creds file and handed to
// the new client.
type IssuedClient struct {
	ID    string
	Creds string
}

// ConnectIssuer dials the bus with an issuer credential (operator or enrollment).
// Unlike Connect it performs no handshake — the issuer is not a directory client —
// so it works for the enrollment identity whose allow-list permits only
// clients.register, and for the operator before any client identity exists.
func ConnectIssuer(ctx context.Context, opts Options) (*Issuer, error) {
	if opts.CredsPath == "" {
		return nil, errors.New("sextant: no issuer credentials (set Options.CredsPath)")
	}
	url := opts.URL
	if url == "" && opts.ConnInfoPath != "" {
		info, err := conninfo.Read(opts.ConnInfoPath)
		if err != nil {
			return nil, err
		}
		url = info.URL
	}
	if url == "" {
		return nil, errors.New("sextant: no bus URL (set Options.URL or Options.ConnInfoPath)")
	}
	// The issuer's id is the reserved name inside its credential (operator/enroll);
	// it is the call subject token the bus authorizes the issuance path against.
	id, _, err := identityFromCreds(opts.CredsPath)
	if err != nil {
		return nil, err
	}
	nc, err := nats.Connect(
		url,
		nats.UserCredentials(opts.CredsPath),
		nats.Name(id),
		// Per-issuer inbox, matching the credential's allow-list (_INBOX.<id>.>) —
		// otherwise the reply to clients.register lands where the allow-list denies.
		nats.CustomInboxPrefix(wireapi.InboxPrefix(id)),
	)
	if err != nil {
		return nil, fmt.Errorf("sextant: issuer connect: %w", err)
	}
	return &Issuer{nc: nc, id: id}, nil
}

func (i *Issuer) call(ctx context.Context, op string, input, out any) error {
	return callConn(ctx, i.nc, i.id, op, input, out)
}

// Register asks the bus to mint a NEW identity with the given display name and
// kind, returning its id and credential. The bus mints and records it; the
// signing keys never leave the bus. Authorization is the issuer's own authority
// (operator = held-identity, enroll = bootstrap/enrollment), enforced by the bus.
func (i *Issuer) Register(ctx context.Context, displayName, kind string) (IssuedClient, error) {
	var out wireapi.RegisterOutput
	if err := i.call(ctx, wireapi.OpClientsRegister, wireapi.RegisterInput{
		DisplayName: displayName,
		Kind:        kind,
	}, &out); err != nil {
		return IssuedClient{}, err
	}
	return IssuedClient{ID: out.ID, Creds: out.Creds}, nil
}

// Retire decommissions an identity for good (operator-only, enforced by the bus):
// it leaves the directory and any live connection is dropped. Distinct from a
// disconnect, which only goes offline.
func (i *Issuer) Retire(ctx context.Context, id string) error {
	return i.call(ctx, wireapi.OpClientsRetire, wireapi.RetireInput{ID: id}, nil)
}

// ListClients returns the directory, like Client.ListClients, for an issuer that
// is authorized to read it (the operator).
func (i *Issuer) ListClients(ctx context.Context) ([]ClientInfo, error) {
	return listClients(ctx, i.call)
}

// SetPrincipal points the bus's principal designation at a client ULID
// (ADR-0030, ADR-0031), as a principal.set call. force authorizes re-pointing an
// ALREADY-established principal; it is ignored on a first claim. The bus enforces
// the asymmetry — only the bootstrap tier may claim an unclaimed principal (and
// the enrollment path only to a client seat), and only the operator may re-point
// an established one — so an agent can never claim or alter the designation.
func (i *Issuer) SetPrincipal(ctx context.Context, principal string, force bool) error {
	return i.call(ctx, wireapi.OpPrincipalSet, wireapi.PrincipalSetInput{Principal: principal, Force: force}, nil)
}

// GetPrincipal reads the current principal ULID (ADR-0030) over the operator
// connection, for an operator-credentialed `principal get` (the operator is not a
// directory client, so it cannot run the full Client handshake).
func (i *Issuer) GetPrincipal(ctx context.Context) (string, error) {
	var out wireapi.PrincipalGetOutput
	if err := i.call(ctx, wireapi.OpPrincipalGet, wireapi.PrincipalGetInput{}, &out); err != nil {
		return "", err
	}
	return out.Principal, nil
}

// Close closes the issuer connection.
func (i *Issuer) Close() error {
	i.nc.Close()
	return nil
}
