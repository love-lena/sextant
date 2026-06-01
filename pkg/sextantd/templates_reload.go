package sextantd

import (
	"context"
	"fmt"

	"github.com/love-lena/sextant/pkg/templates"
)

// ControlTemplatesReloadSubject is the NATS subject the daemon
// subscribes to in order to receive `sextant templates reload`
// requests. The operator CLI publishes one request with a NATS reply
// inbox; the daemon answers with a JSON-encoded TemplatesReloadResponse.
//
// Wire shape uses native NATS request/reply (msg.Respond) rather than
// the full RPC envelope — the operator surface here is "rerun the same
// sync sextantd does at startup," not a verb that participates in
// idempotency caching or audit. Lightweight semantics keep the surface
// from accreting infrastructure it doesn't need.
//
// See slug:feat-templates-reload-cli-verb.
const ControlTemplatesReloadSubject = "sextant.control.templates_reload"

// TemplatesReloadRequest is the inbound payload. Empty today; the
// daemon's templates dir is configured server-side via
// `paths.templates_dir`. A future revision might add an explicit
// override field for tests; keep the struct so we don't have to break
// the wire shape later.
type TemplatesReloadRequest struct{}

// TemplatesReloadResponse is the outbound payload. Exactly one of
// (Count, Error) is meaningful: a success carries Count > 0 and an
// empty Error; a failure carries Error and Count is undefined.
type TemplatesReloadResponse struct {
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

// ReloadTemplates runs templates.SyncDirToKV against the supplied KV
// bucket and templates directory, returning the count of templates
// pushed into KV. This is the function both the
// `sextant.control.templates_reload` subscriber (cmd/sextantd) and the
// MCP `templates_reload` tool (pkg/mcpserver) call into — keeping the
// reload semantics single-source so a CLI-driven reload and an
// agent-driven reload are byte-for-byte identical.
func ReloadTemplates(ctx context.Context, kv templates.KV, dir string) (int, error) {
	if kv == nil {
		return 0, fmt.Errorf("templates reload: kv is nil")
	}
	if dir == "" {
		return 0, fmt.Errorf("templates reload: dir is empty")
	}
	tpls, err := templates.SyncDirToKV(ctx, kv, dir)
	if err != nil {
		return 0, err
	}
	return len(tpls), nil
}
