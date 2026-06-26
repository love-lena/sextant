package sextant

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/nats-io/nats.go"
)

// call invokes a bus operation over the Wire API (ADR-0019): it sends input as a
// request to sx.api.<id>.<op> and decodes the reply into out (nil to ignore the
// result). The client's id is the subject token, which the bus reads as the
// frame author — so the SDK never stamps identity itself. A bus-side failure
// comes back as Response.Error and is returned as an error.
func (c *Client) call(ctx context.Context, op string, input, out any) error {
	return callConn(ctx, c.nc, c.id, op, input, out)
}

// busError is a failure the bus itself replied with (wireapi.Response.Error):
// the request reached the bus and was definitively answered. Its presence (via
// errors.As) distinguishes a bus-side refusal from a transport failure — a
// timeout or dropped connection where the bus never answered and the outcome is
// unknown. The reconnect resume path keys its fatal/retry decision on this type,
// never on error-string matching.
type busError struct {
	op  string
	msg string
}

func (e *busError) Error() string { return fmt.Sprintf("sextant: %s: %s", e.op, e.msg) }

// callConn is the connection-level Wire API call shared by Client and Issuer: it
// requests sx.api.<id>.<op> on nc and decodes the reply. id is the subject token
// (the caller's authenticated identity); the credential's allow-list binds the
// connection to publishing only under its own <id>, so the bus can trust it.
func callConn(ctx context.Context, nc *nats.Conn, id, op string, input, out any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("sextant: %s: marshal input: %w", op, err)
	}
	msg, err := nc.RequestWithContext(ctx, wireapi.CallSubject(id, op), data)
	if err != nil {
		return fmt.Errorf("sextant: %s: %w", op, err)
	}
	var resp wireapi.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("sextant: %s: decode reply: %w", op, err)
	}
	if resp.Error != "" {
		return &busError{op: op, msg: resp.Error}
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("sextant: %s: decode result: %w", op, err)
		}
	}
	return nil
}
