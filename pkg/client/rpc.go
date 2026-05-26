package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// RPCError is the typed error returned by Client.RPC when the server
// replied with a structured error. Inspect via errors.As:
//
//	var rerr *client.RPCError
//	if errors.As(err, &rerr) && rerr.Code == sextantproto.ErrCodeAgentNotFound {
//	    ...
//	}
//
// Transport-level failures (timeout on the client side, NATS publish
// error, malformed reply) surface as plain errors that do NOT satisfy
// errors.As(*RPCError). The only RPCError values that come back with
// Code == "timeout" originate from the server side per spec; client-
// side timeouts use the dedicated ErrRPCTimeout sentinel.
type RPCError struct {
	Code    string
	Message string
	Details map[string]any
}

// Error implements error.
func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc %s: %s", e.Code, e.Message)
}

// ErrRPCTimeout is returned by Client.RPC when the per-call timeout
// elapses before a terminal reply arrives. Clients distinguish this
// from a server-emitted RPCError{Code: "timeout"} via errors.Is.
var ErrRPCTimeout = errors.New("client: rpc timeout")

// RPCOption configures a single Client.RPC call.
type RPCOption interface {
	applyRPC(*rpcOptions)
}

type rpcOptions struct {
	timeout        time.Duration
	idempotencyKey string
}

type rpcOptFunc func(*rpcOptions)

func (f rpcOptFunc) applyRPC(o *rpcOptions) { f(o) }

// WithTimeout overrides the default 10s per-call timeout.
func WithTimeout(d time.Duration) RPCOption {
	return rpcOptFunc(func(o *rpcOptions) {
		o.timeout = d
	})
}

// WithIdempotencyKey overrides the auto-generated UUID key. Use this
// when the caller needs retries to collapse onto the same server-side
// dedup window.
func WithIdempotencyKey(key string) RPCOption {
	return rpcOptFunc(func(o *rpcOptions) {
		o.idempotencyKey = key
	})
}

// rpcDefaultTimeout matches specs/protocols/rpc-catalog.md §"Timeouts".
const rpcDefaultTimeout = 10 * time.Second

// RPC calls the named sextant verb and writes the reply into resp.
// args is JSON-marshalled into the request envelope's verb-specific
// payload field; resp receives the JSON-unmarshalled Result on success.
//
// Errors:
//   - Server-returned structured errors surface as *RPCError. Inspect with
//     errors.As.
//   - ErrRPCTimeout when the per-call timeout elapses (separate sentinel
//     from a server-side "timeout" code so callers can tell client and
//     server timeouts apart).
//   - Plain wrapped errors for transport and marshalling failures.
//
// resp may be nil if the caller doesn't care about the result body —
// errors and the success indicator still surface.
func (c *Client) RPC(ctx context.Context, verb string, args, resp any, opts ...RPCOption) error {
	if c.isClosed() {
		return ErrClosed
	}
	if verb == "" {
		return fmt.Errorf("client: RPC: verb is empty")
	}

	var o rpcOptions
	for _, opt := range opts {
		opt.applyRPC(&o)
	}
	if o.timeout <= 0 {
		o.timeout = rpcDefaultTimeout
	}
	if o.idempotencyKey == "" {
		o.idempotencyKey = uuid.NewString()
	}

	subject := "sextant.rpc." + verb

	// Provision an ephemeral reply subject before publishing — the spec
	// requires reply_to is alive when the request lands.
	reply := nats.NewInbox()
	replyCh := make(chan *nats.Msg, 1)
	sub, err := c.nc.ChanSubscribe(reply, replyCh)
	if err != nil {
		return fmt.Errorf("client: rpc subscribe %s: %w", reply, err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	argsRaw, err := marshalArgs(args)
	if err != nil {
		return fmt.Errorf("client: rpc marshal args: %w", err)
	}

	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: c.cfg.Operator.User}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, from, argsRaw)
	env.ReplyTo = &reply
	env.IdempotencyKey = &o.idempotencyKey

	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("client: rpc marshal envelope: %w", err)
	}
	if err := c.nc.Publish(subject, raw); err != nil {
		return fmt.Errorf("client: rpc publish %s: %w", subject, err)
	}
	if err := c.nc.Flush(); err != nil {
		return fmt.Errorf("client: rpc flush: %w", err)
	}

	deadline := time.NewTimer(o.timeout)
	defer deadline.Stop()
	select {
	case msg, ok := <-replyCh:
		if !ok {
			return fmt.Errorf("client: rpc reply channel closed before terminal")
		}
		return decodeReply(msg.Data, resp)
	case <-deadline.C:
		return ErrRPCTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// marshalArgs handles the nil-args case (verbs with no required args)
// by emitting "{}" so the server's payload parse still works.
func marshalArgs(args any) (json.RawMessage, error) {
	if args == nil {
		return json.RawMessage("{}"), nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// decodeReply parses one rpc_response envelope. Returns *RPCError if the
// reply carries a structured error, transport errors otherwise.
func decodeReply(data []byte, resp any) error {
	var env sextantproto.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("client: rpc decode envelope: %w", err)
	}
	if env.Kind != sextantproto.KindRPCResponse {
		return fmt.Errorf("client: rpc reply kind = %q, want %q",
			env.Kind, sextantproto.KindRPCResponse)
	}
	var payload sextantproto.RPCResponse
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return fmt.Errorf("client: rpc decode payload: %w", err)
	}
	if !payload.Terminal {
		// M7 ships no streaming verbs; surface the protocol violation
		// rather than block forever waiting for a Terminal: true frame.
		return fmt.Errorf("client: rpc non-terminal reply received (streaming not supported in M7)")
	}
	if payload.Error != nil {
		return &RPCError{
			Code:    payload.Error.Code,
			Message: payload.Error.Message,
			Details: payload.Error.Details,
		}
	}
	if resp == nil {
		return nil
	}
	if len(payload.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload.Result, resp); err != nil {
		return fmt.Errorf("client: rpc decode result: %w", err)
	}
	return nil
}
