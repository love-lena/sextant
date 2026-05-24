package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// Publish sends env on subject via core NATS (not JetStream — events
// are persisted by the configured JetStream stream subscription on the
// subject). The envelope is validated before publish so a missing
// TraceID or invalid Kind fails on the publisher, not on every
// downstream consumer.
//
// Publish does not wait for an ack. For RPC, use RPC.
func (c *Client) Publish(ctx context.Context, subject string, env sextantproto.Envelope) error {
	if c.isClosed() {
		return ErrClosed
	}
	if subject == "" {
		return fmt.Errorf("client: Publish requires a non-empty subject")
	}
	if env.Ts.IsZero() {
		env.Ts = sextantproto.NowTimestamp()
	}
	if env.ProtoVersion == "" {
		env.ProtoVersion = sextantproto.ProtoVersion
	}
	if err := env.Validate(); err != nil {
		return fmt.Errorf("client: Publish: %w", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("client: marshal envelope: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.nc.Publish(subject, raw); err != nil {
		return fmt.Errorf("client: publish %s: %w", subject, err)
	}
	return nil
}
