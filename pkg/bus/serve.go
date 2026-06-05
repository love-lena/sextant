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
// (<clientID>). Until the per-client allow-list credential is in place (the SDK
// cutover), a client could publish under another id; afterwards NATS enforces
// that a client may publish only under its own prefix, so the stamped author is
// unforgeable. The serving logic here is identical either way — it always trusts
// the subject token, which the credential makes trustworthy.

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
	b.connected = make(map[string]struct{})
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
	case wireapi.OpArtifactDelete:
		return b.opArtifactDelete(ctx, data)
	case wireapi.OpClientsList:
		return b.opClientsList(ctx)
	case wireapi.OpClientsRegister:
		return b.opClientsRegister(ctx, clientID, data)
	case wireapi.OpClientsDeregister:
		return b.opClientsDeregister(ctx, clientID)
	case wireapi.OpMessageSubscribe:
		return b.opSubscribe(clientID, data)
	case wireapi.OpArtifactWatch:
		return b.opArtifactWatch(clientID, data)
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
			continue // skip an undecodable entry rather than fail the whole batch
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

// --- clients registry ---

func (b *Bus) opClientsList(ctx context.Context) (json.RawMessage, error) {
	keys, err := b.backend.Keys(ctx, sx.BucketClients)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.list: %w", err)
	}
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
			continue // skip a corrupt entry rather than fail the listing
		}
		e.ID = k // the registry key is the authoritative id, not the record body
		out.Clients = append(out.Clients, e)
	}
	sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].ID < out.Clients[j].ID })
	return json.Marshal(out)
}

// opClientsRegister is the write half of the directory (the connect handshake).
// The bus owns the record: it keys it under the call's subject token (the
// authoritative id) and stamps connected_at with its own clock. It registers the
// client only if its epoch matches the bus's; an incompatible client still gets
// the bus epoch back so the SDK fails loud without ever entering the directory.
func (b *Bus) opClientsRegister(ctx context.Context, clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.RegisterInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: clients.register: bad input: %w", err)
	}
	busEpoch, err := b.readEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("bus: clients.register: %w", err)
	}
	connectedAt := nowRFC3339()
	if in.Epoch == busEpoch {
		rec, err := json.Marshal(wireapi.ClientEntry{
			ID:          clientID,
			DisplayName: in.DisplayName,
			Kind:        in.Kind,
			Epoch:       in.Epoch,
			SDK:         in.SDK,
			ConnectedAt: connectedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("bus: clients.register: encode record: %w", err)
		}
		if _, err := b.backend.Put(ctx, sx.BucketClients, clientID, rec); err != nil {
			return nil, fmt.Errorf("bus: clients.register: %w", err)
		}
		b.addConnected(clientID)
	}
	return json.Marshal(wireapi.RegisterOutput{BusEpoch: busEpoch, ConnectedAt: connectedAt})
}

// opClientsDeregister is the leave half (Close). Keyed under the caller's own
// subject token, so a client can only remove its own entry.
func (b *Bus) opClientsDeregister(ctx context.Context, clientID string) (json.RawMessage, error) {
	b.removeConnected(clientID)
	if err := b.backend.Delete(ctx, sx.BucketClients, clientID); err != nil {
		return nil, fmt.Errorf("bus: clients.deregister: %w", err)
	}
	return json.Marshal(struct{}{})
}

// readEpoch reads the bus's protocol epoch from the public meta bucket (the value
// bootstrap wrote). register returns it so the SDK hard-gates against it.
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

func (b *Bus) addConnected(id string) {
	b.connectedMu.Lock()
	b.connected[id] = struct{}{}
	b.connectedMu.Unlock()
}

func (b *Bus) removeConnected(id string) {
	b.connectedMu.Lock()
	delete(b.connected, id)
	b.connectedMu.Unlock()
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
