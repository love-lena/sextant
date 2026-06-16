package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/love-lena/sextant/internal/backend"
	"github.com/love-lena/sextant/internal/backend/natsbackend"
	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
)

// The bus serves the protocol's operations (ADR-0018, ADR-0019): a client makes
// a NATS request to sx.api.<clientID>.<op>, the bus serves it against the backend
// interface, stamps the frame, and replies. This file is the request/reply
// (one-shot + pull-batch) operations: message.publish/read, the artifact
// operations, and clients.list. The push-stream operations (message.subscribe,
// artifact.watch) over sx.deliver land with the SDK cutover.
//
// Author handling: the bus takes the author from the call's subject token
// (<clientID>). The per-client allow-list credential (ADR-0019) lets a client
// publish only under its own sx.api.<id> prefix, so the subject token is the
// authenticated identity and the stamped author is unforgeable — the serving
// logic always trusts the subject token, which the credential makes trustworthy.

const (
	apiMaxConcurrent = 64
	apiCallTimeout   = 30 * time.Second
)

// startServing wires a backend over the operator connection and subscribes to
// the Wire API call space. It runs on the bus's in-process operator connection,
// so it has full access; clients reach it only by request/reply.
func (b *Bus) startServing() error {
	js, err := jetstream.New(b.opConn)
	if err != nil {
		return fmt.Errorf("bus: serve: jetstream: %w", err)
	}
	b.backend = natsbackend.New(js, sx.StreamMessages)
	b.apiSem = make(chan struct{}, apiMaxConcurrent)
	b.relayCtx, b.relayCancel = context.WithCancel(context.Background())
	b.relays = make(map[string]map[string]*relay)
	sub, err := b.opConn.Subscribe(wireapi.WildcardSubject, func(msg *nats.Msg) {
		// Spawn immediately so the NATS dispatcher never blocks (no head-of-line
		// blocking), then bound concurrency by waiting for a worker slot.
		go func() {
			b.apiSem <- struct{}{}
			defer func() { <-b.apiSem }()
			b.handleCall(msg)
		}()
	})
	if err != nil {
		return fmt.Errorf("bus: serve: subscribe %s: %w", wireapi.WildcardSubject, err)
	}
	b.apiSub = sub
	return nil
}

// stopServing tears the API subscription down and cancels every running relay
// (called on Shutdown). Cancelling relayCtx cascades to all per-subscription
// relay contexts, so their backend streams close and their goroutines exit.
func (b *Bus) stopServing() {
	if b.apiSub != nil {
		_ = b.apiSub.Unsubscribe()
	}
	if b.relayCancel != nil {
		b.relayCancel()
	}
}

// handleCall parses, dispatches, and replies to one Wire API request.
func (b *Bus) handleCall(msg *nats.Msg) {
	clientID, op, ok := wireapi.ParseCallSubject(msg.Subject)
	if !ok {
		b.respond(msg, wireapi.Response{Error: "bus: malformed call subject"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiCallTimeout)
	defer cancel()
	result, err := b.dispatch(ctx, clientID, op, msg.Data)
	if err != nil {
		b.respond(msg, wireapi.Response{Error: err.Error()})
		return
	}
	b.respond(msg, wireapi.Response{Result: result})
}

func (b *Bus) respond(msg *nats.Msg, r wireapi.Response) {
	if msg.Reply == "" {
		return // a fire-and-forget call expects no reply
	}
	data, err := json.Marshal(r)
	if err != nil {
		data = []byte(`{"error":"bus: internal: failed to marshal response"}`)
	}
	_ = msg.Respond(data)
}

func (b *Bus) dispatch(ctx context.Context, clientID, op string, data []byte) (json.RawMessage, error) {
	switch op {
	case wireapi.OpMessagePublish:
		return b.opPublish(ctx, clientID, data)
	case wireapi.OpMessageRead:
		return b.opRead(ctx, data)
	case wireapi.OpArtifactCreate:
		return b.opArtifactCreate(ctx, clientID, data)
	case wireapi.OpArtifactUpdate:
		return b.opArtifactUpdate(ctx, clientID, data)
	case wireapi.OpArtifactGet:
		return b.opArtifactGet(ctx, data)
	case wireapi.OpArtifactList:
		return b.opArtifactList(ctx)
	case wireapi.OpArtifactDelete:
		return b.opArtifactDelete(ctx, data)
	case wireapi.OpClientsList:
		return b.opClientsList(ctx)
	case wireapi.OpClientsRegister:
		return b.opClientsRegister(ctx, clientID, data)
	case wireapi.OpClientsRetire:
		return b.opClientsRetire(ctx, clientID, data)
	case wireapi.OpClientsHello:
		return b.opClientsHello(ctx, clientID)
	case wireapi.OpClientsHeartbeat:
		return b.opClientsHeartbeat(ctx, clientID, data)
	case wireapi.OpPrincipalGet:
		return b.opPrincipalGet(ctx)
	case wireapi.OpPrincipalSet:
		return b.opPrincipalSet(ctx, clientID, data)
	case wireapi.OpMessageSubscribe:
		return b.opSubscribe(clientID, data)
	case wireapi.OpArtifactWatch:
		return b.opArtifactWatch(clientID, data)
	case wireapi.OpPrincipalWatch:
		return b.opPrincipalWatch(clientID, data)
	case wireapi.OpSubscriptionStop:
		return b.opSubscriptionStop(clientID, data)
	default:
		return nil, fmt.Errorf("bus: unknown operation %q", op)
	}
}

// --- message operations ---

func (b *Bus) opPublish(ctx context.Context, clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.PublishInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: publish: bad input: %w", err)
	}
	if !strings.HasPrefix(in.Subject, sx.MessagePrefix) {
		return nil, fmt.Errorf("bus: publish subject %q is not in the messages space (%s*)", in.Subject, sx.MessagePrefix)
	}
	frame := wire.Frame{
		ID:     ulid.Make().String(),
		Author: clientID,
		Kind:   wire.KindMessage,
		Epoch:  wire.Epoch,
		Record: in.Record,
	}
	if err := frame.Validate(); err != nil {
		return nil, fmt.Errorf("bus: publish: %w", err)
	}
	fb, err := wire.Encode(frame)
	if err != nil {
		return nil, fmt.Errorf("bus: publish: encode: %w", err)
	}
	seq, err := b.backend.Append(ctx, in.Subject, fb)
	if err != nil {
		return nil, fmt.Errorf("bus: publish: %w", err)
	}
	return json.Marshal(wireapi.PublishOutput{ID: frame.ID, Seq: seq})
}

func (b *Bus) opRead(ctx context.Context, data []byte) (json.RawMessage, error) {
	var in wireapi.ReadInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: read: bad input: %w", err)
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	entries, next, err := b.backend.Read(ctx, in.Subject, in.Since, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("bus: read: %w", err)
	}
	out := wireapi.ReadOutput{Messages: make([]wire.Frame, 0, len(entries)), NextCursor: next}
	for _, e := range entries {
		f, err := wire.Decode(e.Data)
		if err != nil {
			// Skip an undecodable entry rather than fail the whole batch — but
			// say so. Only store corruption or seam-injected bytes reach here.
			b.logf("bus: read: dropping undecodable frame on %s at seq %d: %v", e.Subject, e.Seq, err)
			continue
		}
		out.Messages = append(out.Messages, f)
	}
	return json.Marshal(out)
}

// --- artifact operations ---

func validArtifactRecord(r json.RawMessage) error {
	if len(r) == 0 || !json.Valid(r) {
		return errors.New("artifact record must be a non-empty JSON lexicon")
	}
	return nil
}

func (b *Bus) opArtifactCreate(ctx context.Context, clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.ArtifactCreateInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: artifact.create: bad input: %w", err)
	}
	if err := validArtifactRecord(in.Record); err != nil {
		return nil, fmt.Errorf("bus: artifact.create: %w", err)
	}
	now := nowRFC3339()
	frame := wire.Frame{
		ID:        ulid.Make().String(),
		Author:    clientID,
		Kind:      wire.KindArtifact,
		Epoch:     wire.Epoch,
		Record:    in.Record,
		CreatedAt: now,
		UpdatedAt: now,
	}
	fb, err := wire.Encode(frame)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.create: encode: %w", err)
	}
	rev, err := b.backend.Create(ctx, sx.BucketArtifacts, in.Name, fb)
	if errors.Is(err, backend.ErrKeyExists) {
		return nil, fmt.Errorf("bus: artifact %q already exists", in.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.create: %w", err)
	}
	return json.Marshal(wireapi.ArtifactWriteOutput{Name: in.Name, Revision: rev})
}

func (b *Bus) opArtifactUpdate(ctx context.Context, clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.ArtifactUpdateInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: artifact.update: bad input: %w", err)
	}
	if err := validArtifactRecord(in.Record); err != nil {
		return nil, fmt.Errorf("bus: artifact.update: %w", err)
	}
	cur, _, err := b.backend.Get(ctx, sx.BucketArtifacts, in.Name)
	if errors.Is(err, backend.ErrNotFound) {
		return nil, fmt.Errorf("bus: artifact %q does not exist", in.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.update: %w", err)
	}
	prev, err := wire.Decode(cur)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.update: decode current: %w", err)
	}
	// Preserve the artifact's stable identity and creation time; the author
	// becomes the current writer and updatedAt advances.
	frame := wire.Frame{
		ID:        prev.ID,
		Author:    clientID,
		Kind:      wire.KindArtifact,
		Epoch:     wire.Epoch,
		Record:    in.Record,
		CreatedAt: prev.CreatedAt,
		UpdatedAt: nowRFC3339(),
	}
	fb, err := wire.Encode(frame)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.update: encode: %w", err)
	}
	rev, err := b.backend.CompareAndSet(ctx, sx.BucketArtifacts, in.Name, fb, in.ExpectedRev)
	if errors.Is(err, backend.ErrRevisionMismatch) {
		return nil, fmt.Errorf("bus: artifact %q changed since revision %d", in.Name, in.ExpectedRev)
	}
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.update: %w", err)
	}
	return json.Marshal(wireapi.ArtifactWriteOutput{Name: in.Name, Revision: rev})
}

func (b *Bus) opArtifactGet(ctx context.Context, data []byte) (json.RawMessage, error) {
	var in wireapi.ArtifactGetInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: artifact.get: bad input: %w", err)
	}
	val, rev, err := b.backend.Get(ctx, sx.BucketArtifacts, in.Name)
	if errors.Is(err, backend.ErrNotFound) {
		return nil, fmt.Errorf("bus: artifact %q does not exist", in.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.get: %w", err)
	}
	frame, err := wire.Decode(val)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.get: decode: %w", err)
	}
	return json.Marshal(wireapi.ArtifactGetOutput{
		Name:      in.Name,
		Record:    frame.Record,
		Revision:  rev,
		CreatedAt: frame.CreatedAt,
		UpdatedAt: frame.UpdatedAt,
	})
}

// opArtifactList is the artifacts directory read: the name and bus-stamped
// metadata of every artifact in the ARTIFACTS bucket, sorted by name. It is
// discovery of state the bus already owns (ADR-0016) — a client lists, then
// artifact.gets the one it wants — so it reads each key's metadata (revision +
// the stamped create/update times) without returning the records. A key left
// between the listing and the per-key read is skipped, as is an undecodable
// frame, rather than failing the whole listing for everyone. An empty bucket is
// an empty slice, not an error.
func (b *Bus) opArtifactList(ctx context.Context) (json.RawMessage, error) {
	keys, err := b.backend.Keys(ctx, sx.BucketArtifacts)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.list: %w", err)
	}
	out := wireapi.ArtifactListOutput{Artifacts: make([]wireapi.ArtifactListEntry, 0, len(keys))}
	for _, k := range keys {
		val, rev, err := b.backend.Get(ctx, sx.BucketArtifacts, k)
		if errors.Is(err, backend.ErrNotFound) {
			continue // deleted between the key listing and this read
		}
		if err != nil {
			return nil, fmt.Errorf("bus: artifact.list: read %q: %w", k, err)
		}
		frame, err := wire.Decode(val)
		if err != nil {
			// Skip an undecodable frame rather than fail the listing — but say so.
			b.logf("bus: artifact.list: skipping artifact %q at revision %d: undecodable frame: %v", k, rev, err)
			continue
		}
		out.Artifacts = append(out.Artifacts, wireapi.ArtifactListEntry{
			Name:      k,
			Revision:  rev,
			CreatedAt: frame.CreatedAt,
			UpdatedAt: frame.UpdatedAt,
		})
	}
	sort.Slice(out.Artifacts, func(i, j int) bool { return out.Artifacts[i].Name < out.Artifacts[j].Name })
	return json.Marshal(out)
}

func (b *Bus) opArtifactDelete(ctx context.Context, data []byte) (json.RawMessage, error) {
	var in wireapi.ArtifactDeleteInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: artifact.delete: bad input: %w", err)
	}
	if err := b.backend.Delete(ctx, sx.BucketArtifacts, in.Name); err != nil {
		return nil, fmt.Errorf("bus: artifact.delete: %w", err)
	}
	return json.Marshal(struct{}{})
}

// --- clients registry (ADR-0020: a durable store of bus-issued identities,
// joined at read time with connection-derived presence) ---

// connzLimit bounds how many connections the presence query inspects. The MVP
// reads a single page; far more than any single-host deployment will reach.
const connzLimit = 4096

// opClientsList is the directory read (ADR-0020): the join of the durable identity
// records with live presence. It lists every issued identity — online and offline
// — and stamps each with a derived presence computed from the connection table,
// never from a stored field. Offline clients are shown by default; that durable
// directory is the point. The authenticated subject is the internal join key and
// is dropped from the client-facing reply.
func (b *Bus) opClientsList(ctx context.Context) (json.RawMessage, error) {
	keys, err := b.backend.Keys(ctx, sx.BucketClients)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.list: %w", err)
	}
	online, err := b.onlineSubjects()
	if err != nil {
		return nil, fmt.Errorf("bus: clients.list: %w", err)
	}
	now := time.Now().UTC() // one clock read for the whole listing's freshness join
	out := wireapi.ClientsListOutput{Clients: make([]wireapi.ClientEntry, 0, len(keys))}
	for _, k := range keys {
		val, _, err := b.backend.Get(ctx, sx.BucketClients, k)
		if errors.Is(err, backend.ErrNotFound) {
			continue // left between the key listing and this read
		}
		if err != nil {
			return nil, fmt.Errorf("bus: clients.list: read %q: %w", k, err)
		}
		var e wireapi.ClientEntry
		if err := json.Unmarshal(val, &e); err != nil {
			// Skip a corrupt record rather than fail the listing — but say so.
			b.logf("bus: clients.list: skipping corrupt registry record %q: %v", k, err)
			continue
		}
		e.ID = k // the registry key is the authoritative id, not the record body
		// Dual-source presence (TASK-126): online if the connection table shows it
		// (the first-hand, leaf-blind view) OR its last heartbeat is fresh (the
		// leaf-correct view that survives a leaf link Connz cannot see across). A
		// client behind a leaf is online to its peers while it keeps beating, even
		// though this server holds no connection for it.
		connOnline := e.Subject != "" && online[e.Subject]
		e.Presence = wireapi.PresenceOffline
		if connOnline || b.heartbeatFresh(e.LastSeen, now) {
			e.Presence = wireapi.PresenceOnline
		}
		e.Subject = "" // internal join key — not part of the client-facing directory
		// LastSeen stays on the reply: it is not sensitive (unlike Subject) and a
		// consumer may render it ("last seen 30s ago") alongside the derived Presence.
		out.Clients = append(out.Clients, e)
	}
	sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].ID < out.Clients[j].ID })
	return json.Marshal(out)
}

// opClientsRegister is the issuance path (ADR-0020): the exception to "you must
// already be someone." The caller asks the bus to mint a NEW identity (it does
// not name itself — the bus generates the id). Who may mint:
//   - OperatorID — held-identity mode: mint for another, any kind (ADR-0020).
//   - EnrollID — bootstrap/enrollment mode: mint for self (ADR-0020).
//   - any registered client — mint-on-behalf (ADR-0033) — EXCEPT a spawned
//     worker. The fence is inverted from an allowlist: rather than blessing a
//     dispatcher kind (kind is weakly enforced), the bus denies only a client it
//     itself spawned (ClientEntry.SpawnedBy set). So any top-level client can act
//     as a dispatcher, but the workers it spawns cannot recursively dispatch —
//     the bus stamps each mint-on-behalf child with SpawnedBy=caller.
//
// Either way the bus then does the same thing: mint the credential, persist the
// durable record, return the id and creds.
func (b *Bus) opClientsRegister(ctx context.Context, callerID string, data []byte) (json.RawMessage, error) {
	var in wireapi.RegisterInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: clients.register: bad input: %w", err)
	}
	var spawnedBy string
	switch callerID {
	case wireapi.OperatorID, wireapi.EnrollID:
		// Held-identity / bootstrap authority — a top-level mint, not a spawned worker.
	default:
		// mint-on-behalf: any client may dispatch unless it is itself a spawned
		// worker. Mark the child as spawned by this caller (lineage + the fence
		// that stops the child from recursively dispatching).
		if !b.callerMayDispatch(ctx, callerID) {
			return nil, fmt.Errorf("bus: clients.register: caller %q may not dispatch new clients (a spawned worker cannot; mint-on-behalf, ADR-0033)", callerID)
		}
		spawnedBy = callerID
	}
	creds, id, err := b.mintClient(ctx, in.DisplayName, in.Kind, spawnedBy)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.register: %w", err)
	}
	return json.Marshal(wireapi.RegisterOutput{ID: id, Creds: creds})
}

// callerMayDispatch reports whether callerID is allowed to mint children
// (mint-on-behalf, ADR-0033). The rule is inverted from an allowlist: every
// registered client may dispatch EXCEPT one the bus itself spawned on another
// client's behalf — marked by a SpawnedBy in its durable record, a bus-stamped
// field that does not depend on the weakly-enforced kind. Fail closed: a missing
// or unreadable record cannot be confirmed top-level, so it may not dispatch.
func (b *Bus) callerMayDispatch(ctx context.Context, callerID string) bool {
	val, _, err := b.backend.Get(ctx, sx.BucketClients, callerID)
	if err != nil {
		return false
	}
	var e wireapi.ClientEntry
	if err := json.Unmarshal(val, &e); err != nil {
		return false
	}
	return e.SpawnedBy == ""
}

// opClientsRetire decommissions an identity for good (ADR-0020): operator-only. It
// deletes the durable record — so the identity leaves the directory — and kicks any
// live connection for it. This is distinct from a disconnect, which only drops
// presence to offline; retire is a deliberate end of life.
func (b *Bus) opClientsRetire(ctx context.Context, callerID string, data []byte) (json.RawMessage, error) {
	if callerID != wireapi.OperatorID {
		return nil, fmt.Errorf("bus: clients.retire: only the operator may retire an identity")
	}
	var in wireapi.RetireInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: clients.retire: bad input: %w", err)
	}
	if in.ID == "" {
		return nil, errors.New("bus: clients.retire: id is required")
	}
	// Capture the subject before deleting, so the live connection can be kicked.
	var subject string
	if val, _, err := b.backend.Get(ctx, sx.BucketClients, in.ID); err == nil {
		var e wireapi.ClientEntry
		if json.Unmarshal(val, &e) == nil {
			subject = e.Subject
		}
	}
	if err := b.backend.Delete(ctx, sx.BucketClients, in.ID); err != nil {
		return nil, fmt.Errorf("bus: clients.retire: %w", err)
	}
	if subject != "" {
		b.disconnectSubject(subject) // best-effort: stop a retired client operating now
	}
	return json.Marshal(struct{}{})
}

// opClientsHello is the connect handshake (ADR-0020). A connecting client calls it
// once to (a) confirm it is a known identity — a caller with no durable record was
// never issued, or has been retired, and is rejected, which makes retire effective
// even before the old credential is revoked — and (b) fold the protocol-epoch
// hard-gate into one round-trip (it returns the bus epoch the SDK exact-matches and
// the bus-stamped server time for the clock-skew announce). It asserts no presence:
// online/offline is derived from the connection itself.
func (b *Bus) opClientsHello(ctx context.Context, callerID string) (json.RawMessage, error) {
	if _, _, err := b.backend.Get(ctx, sx.BucketClients, callerID); err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return nil, fmt.Errorf("bus: identity %q is not registered (or has been retired)", callerID)
		}
		return nil, fmt.Errorf("bus: clients.hello: %w", err)
	}
	epoch, err := b.readEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.hello: %w", err)
	}
	// Fold the principal designation into the handshake so a client discovers it
	// on connect (ADR-0030), the same way it learns the epoch — one round-trip,
	// no separate read. A connected client then keeps it current with
	// principal.watch (it never reads the KV directly; the allow-list forbids it).
	principal, err := b.readPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.hello: %w", err)
	}
	return json.Marshal(wireapi.HelloOutput{BusEpoch: epoch, ServerTime: nowRFC3339(), Principal: principal})
}

// opClientsHeartbeat is the periodic liveness signal (TASK-126). It does two
// things on each beat and returns the bus-stamped time:
//
//   - Presence floor: it stamps a bus-clock last_seen on the caller's registry
//     record (same clock source as IssuedAt). last_seen is the leaf-correct
//     presence source — unlike Connz it survives a leaf link — so a connected
//     client a Connz query cannot see is still derived online while its beats
//     are fresh. The write is last-writer-wins (the registry bucket is History:1
//     and a beat is idempotent overwrite-with-a-newer-time): no compare-and-set
//     retry loop, because two concurrent beats from the same client only ever
//     advance the time, and a beat racing a register/retire is bounded by the
//     same not-found gate hello uses.
//   - Push-path floor: it core-NATS publishes a HeartbeatEcho (carrying the
//     caller's Seq) to the dedicated, transient sx.hb.<id> subject. The client
//     auto-subscribes its own and confirms the echo arrives — a beat sent but
//     not echoed within the window is a stale push path (TASK-124 mode-D). This
//     is a plain core publish, not a JetStream/delivery relay: it must not
//     persist or queue, so a missed echo signals a dead path rather than piling
//     up.
//
// A caller with no durable record (never issued, or retired) is rejected, like
// hello — the record is what it modifies, so there is nothing to stamp.
func (b *Bus) opClientsHeartbeat(ctx context.Context, callerID string, data []byte) (json.RawMessage, error) {
	var in wireapi.HeartbeatInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: clients.heartbeat: bad input: %w", err)
	}
	val, _, err := b.backend.Get(ctx, sx.BucketClients, callerID)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return nil, fmt.Errorf("bus: identity %q is not registered (or has been retired)", callerID)
		}
		return nil, fmt.Errorf("bus: clients.heartbeat: %w", err)
	}
	var e wireapi.ClientEntry
	if err := json.Unmarshal(val, &e); err != nil {
		return nil, fmt.Errorf("bus: clients.heartbeat: decode record %q: %w", callerID, err)
	}
	now := nowRFC3339()
	e.LastSeen = now
	rec, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.heartbeat: encode record: %w", err)
	}
	// Last-writer-wins: the registry bucket keeps one revision per client, and a
	// beat only ever advances last_seen, so an unconditional Put is correct — no
	// CAS retry loop (two beats racing both write a fresh time; either wins).
	if _, err := b.backend.Put(ctx, sx.BucketClients, callerID, rec); err != nil {
		return nil, fmt.Errorf("bus: clients.heartbeat: persist last_seen: %w", err)
	}
	// Echo the beat back on the caller's dedicated heartbeat subject — a transient
	// core publish (no persistence). Best-effort: a publish error does not fail the
	// beat (last_seen is already recorded), but it must be loud.
	echo, err := json.Marshal(wireapi.HeartbeatEcho{Seq: in.Seq})
	if err != nil {
		return nil, fmt.Errorf("bus: clients.heartbeat: encode echo: %w", err)
	}
	if err := b.opConn.Publish(wireapi.HeartbeatSubject(callerID), echo); err != nil {
		b.logf("bus: clients.heartbeat: echo publish to %s failed: %v", wireapi.HeartbeatSubject(callerID), err)
	}
	return json.Marshal(wireapi.HeartbeatOutput{ServerTime: now})
}

// opPrincipalGet returns the current principal ULID (ADR-0030). Any authenticated
// caller may read it — the read-open half of the key's shape.
func (b *Bus) opPrincipalGet(ctx context.Context) (json.RawMessage, error) {
	principal, err := b.readPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("bus: principal.get: %w", err)
	}
	return json.Marshal(wireapi.PrincipalGetOutput{Principal: principal})
}

// opPrincipalSet points the principal at a client seat (ADR-0030, ADR-0031). Its
// authorization is asymmetric around whether the principal is still unclaimed —
// the bus enforces both halves:
//
//   - Claiming the unclaimed default (the bootstrap operator seat) is open to the
//     bootstrap tier (operator or enrollment credential) and only to a kind=client
//     target. This is what makes `register --self` self-designating, and it keeps
//     the human-only guarantee at the source: an agent (kind=agent) can never be
//     claimed, even by the bootstrap tier.
//   - Re-pointing an established principal is operator-only and force-gated, so
//     moving operator-equivalence takes intent rather than a casual overwrite. A
//     client-tier ULID caller can do neither — an agent can never claim or alter
//     the designation (the spine of ADR-0030).
//
// The write is a compare-and-set at the revision read, so a concurrent first
// claim resolves to a single winner. The value is stored verbatim on a re-point
// (the two-way door — a wrong value is corrected by another set).
func (b *Bus) opPrincipalSet(ctx context.Context, callerID string, data []byte) (json.RawMessage, error) {
	var in wireapi.PrincipalSetInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: principal.set: bad input: %w", err)
	}
	if in.Principal == "" {
		return nil, errors.New("bus: principal.set: principal is required")
	}
	cur, rev, err := b.backend.Get(ctx, sx.BucketMeta, sx.MetaKeyPrincipal)
	if err != nil {
		return nil, fmt.Errorf("bus: principal.set: read current designation: %w", err)
	}
	current := string(cur)

	if current == wireapi.OperatorID {
		// Unclaimed: a frictionless first claim by the bootstrap tier.
		switch callerID {
		case wireapi.OperatorID:
			// The operator claims verbatim — its two-way door (ADR-0030).
		case wireapi.EnrollID:
			// The open enroll path (what `register --self` rides): enforce the
			// human-only guarantee at the source, so an auto-enrolling agent can
			// never claim itself.
			if err := b.requireNonAgentSeat(ctx, in.Principal); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("bus: principal.set: only the operator or enrollment credential may claim the principal")
		}
	} else {
		// Established: a deliberate re-point — operator only, force-gated.
		if callerID != wireapi.OperatorID {
			return nil, fmt.Errorf("bus: principal.set: only the operator may re-point an established principal")
		}
		if current != in.Principal && !in.Force {
			return nil, fmt.Errorf("bus: principal.set: principal is already designated (%s); pass force to re-point", current)
		}
	}

	if _, err := b.backend.CompareAndSet(ctx, sx.BucketMeta, sx.MetaKeyPrincipal, []byte(in.Principal), rev); err != nil {
		return nil, fmt.Errorf("bus: principal.set: %w", err)
	}
	if current != in.Principal {
		// The audit half of "loud": connected clients also observe the move live via
		// principal.watch; this records who moved it and from where.
		b.logf("bus: principal designation %s -> %s (by %s)", current, in.Principal, callerID)
	}
	return json.Marshal(wireapi.PrincipalSetOutput{Principal: in.Principal})
}

// requireNonAgentSeat verifies id names a registered, non-agent seat — the
// human-only guarantee enforced at the source (ADR-0031). The principal is a
// human's seat; kinds are otherwise open (a fork may label human seats
// "client", "human", a role, …), so the bus rejects exactly one kind here: an
// auto-minting agent (KindAgent) can never be claimed as the principal, even via
// the open enrollment path.
func (b *Bus) requireNonAgentSeat(ctx context.Context, id string) error {
	val, _, err := b.backend.Get(ctx, sx.BucketClients, id)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return fmt.Errorf("bus: principal.set: %q is not a registered client", id)
		}
		return fmt.Errorf("bus: principal.set: look up %q: %w", id, err)
	}
	var e wireapi.ClientEntry
	if err := json.Unmarshal(val, &e); err != nil {
		return fmt.Errorf("bus: principal.set: decode client %q: %w", id, err)
	}
	if e.Kind == wireapi.KindAgent {
		return fmt.Errorf("bus: principal.set: %q is an agent, not a human seat; an agent can never be the principal", id)
	}
	return nil
}

// onlineSubjects returns the set of authenticated public keys with a live
// connection right now — the bus's first-hand presence view from its own
// connection table (ADR-0020). Because the bus is the embedded server, this is
// authoritative: ConnInfo.AuthorizedUser is the JWT subject NATS verified, so a
// client cannot spoof another's presence. A client record is online iff its
// stored subject is in this set.
func (b *Bus) onlineSubjects() (map[string]bool, error) {
	// Username:true asks Connz to populate AuthorizedUser (the authenticated JWT
	// subject); without it that field is empty and presence cannot be joined.
	cz, err := b.ns.Connz(&natsserver.ConnzOptions{Username: true, Limit: connzLimit})
	if err != nil {
		return nil, fmt.Errorf("connz: %w", err)
	}
	set := make(map[string]bool, len(cz.Conns))
	for _, c := range cz.Conns {
		if c.AuthorizedUser != "" {
			set[c.AuthorizedUser] = true
		}
	}
	return set, nil
}

// heartbeatFresh reports whether lastSeen (RFC3339, the bus-stamped time of a
// client's most recent heartbeat) is within the freshness window as of now — the
// OR-half of the dual-source presence rule (TASK-126). An empty or unparseable
// last_seen is not fresh (a client that has never beaten, or a pre-TASK-126
// record): presence then rests on the connection table alone. A future beat
// (clock skew) still reads as fresh — the window is one-sided on age.
func (b *Bus) heartbeatFresh(lastSeen string, now time.Time) bool {
	if lastSeen == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil {
		return false
	}
	return now.Sub(t) < b.freshnessWindow
}

// onlineClientIDs returns the ids of the clients connected right now — the join of
// the durable records with live presence — for Drain to target.
func (b *Bus) onlineClientIDs(ctx context.Context) ([]string, error) {
	online, err := b.onlineSubjects()
	if err != nil {
		return nil, err
	}
	keys, err := b.backend.Keys(ctx, sx.BucketClients)
	if err != nil {
		return nil, fmt.Errorf("clients keys: %w", err)
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		val, _, err := b.backend.Get(ctx, sx.BucketClients, k)
		if err != nil {
			if !errors.Is(err, backend.ErrNotFound) { // not-found = deleted between listing and read (benign)
				b.logf("bus: drain: skipping client %q: read record: %v", k, err)
			}
			continue
		}
		var e wireapi.ClientEntry
		if err := json.Unmarshal(val, &e); err != nil {
			// A corrupt record means this client cannot be drain-targeted — say so.
			b.logf("bus: drain: skipping corrupt registry record %q: %v", k, err)
			continue
		}
		if e.Subject != "" && online[e.Subject] {
			ids = append(ids, k)
		}
	}
	return ids, nil
}

// disconnectSubject best-effort closes any live connection authenticated as
// subject, so a retire takes effect on an already-connected client. Best-effort:
// a connection already gone, or a transient Connz error, is not a retire failure —
// the record is deleted regardless, which removes the identity from the directory.
func (b *Bus) disconnectSubject(subject string) {
	cz, err := b.ns.Connz(&natsserver.ConnzOptions{Username: true, Limit: connzLimit})
	if err != nil {
		return
	}
	for _, c := range cz.Conns {
		if c.AuthorizedUser == subject {
			_ = b.ns.DisconnectClientByID(c.Cid)
		}
	}
}

// readEpoch reads the bus's protocol epoch from the public meta bucket (the value
// bootstrap wrote). The connect handshake returns it so the SDK hard-gates on it.
func (b *Bus) readEpoch(ctx context.Context) (int, error) {
	val, _, err := b.backend.Get(ctx, sx.BucketMeta, sx.MetaKeyEpoch)
	if err != nil {
		return 0, fmt.Errorf("read epoch: %w", err)
	}
	n, err := strconv.Atoi(string(val))
	if err != nil {
		return 0, fmt.Errorf("bad epoch %q: %w", val, err)
	}
	return n, nil
}

// readPrincipal reads the bus's principal designation from the public meta bucket
// (ADR-0030). An absent key (a fork that never defaulted it, or one cleared by an
// operator) is not an error — it reads as the empty string, "no principal".
func (b *Bus) readPrincipal(ctx context.Context) (string, error) {
	val, _, err := b.backend.Get(ctx, sx.BucketMeta, sx.MetaKeyPrincipal)
	if errors.Is(err, backend.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read principal: %w", err)
	}
	return string(val), nil
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
