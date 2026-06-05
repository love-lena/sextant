package sextant

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/love-lena/sextant/internal/wireapi"
)

// call invokes a bus operation over the Wire API (ADR-0019): it sends input as a
// request to sx.api.<id>.<op> and decodes the reply into out (nil to ignore the
// result). The client's id is the subject token, which the bus reads as the
// frame author — so the SDK never stamps identity itself. A bus-side failure
// comes back as Response.Error and is returned as an error.
func (c *Client) call(ctx context.Context, op string, input, out any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("sextant: %s: marshal input: %w", op, err)
	}
	msg, err := c.nc.RequestWithContext(ctx, wireapi.CallSubject(c.id, op), data)
	if err != nil {
		return fmt.Errorf("sextant: %s: %w", op, err)
	}
	var resp wireapi.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return fmt.Errorf("sextant: %s: decode reply: %w", op, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("sextant: %s: %s", op, resp.Error)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("sextant: %s: decode result: %w", op, err)
		}
	}
	return nil
}
