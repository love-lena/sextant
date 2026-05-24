package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// Audit subject prefixes per specs/protocols/bus-subjects.md.
const (
	subjectAuditRPC       = "audit.rpc"
	subjectAuditRPCResult = "audit.rpc_result"
)

// auditPublisher emits the two audit envelopes the spec requires per
// RPC: one "audit.rpc" before dispatch and one "audit.rpc_result"
// after completion. Both share the request's TraceID so a downstream
// query can correlate the pair.
//
// Publishes are best-effort: the server logs but does not surface
// audit-publish failures to the caller — refusing to dispatch because
// the audit stream is unavailable would put the daemon in a worse
// state than emitting the reply.
type auditPublisher struct {
	nc   *nats.Conn
	from sextantproto.Address
}

func newAuditPublisher(nc *nats.Conn, from sextantproto.Address) *auditPublisher {
	return &auditPublisher{nc: nc, from: from}
}

// PreDispatch publishes the pre-dispatch audit envelope. The fields
// match specs/protocols/rpc-catalog.md §"Audit": verb, from,
// idempotency_key, capability_required, allowed.
func (a *auditPublisher) PreDispatch(_ context.Context, req sextantproto.Envelope, verb, cap string, allowed bool) error {
	details := map[string]any{
		"verb":                verb,
		"from_kind":           string(req.From.Kind),
		"from_id":             req.From.ID,
		"idempotency_key":     derefString(req.IdempotencyKey),
		"capability_required": cap,
		"allowed":             allowed,
	}
	payload := sextantproto.AuditPayload{
		Actor:              auditActor(req.From),
		Action:             "rpc." + verb,
		CapabilityRequired: cap,
		Result:             resultFor(allowed),
		Details:            details,
	}
	env := buildAuditEnvelope(a.from, req, payload)
	return a.publish(subjectAuditRPC, env)
}

// PostDispatch publishes the post-completion audit envelope. terminal
// is one of "success" | "error" | "stream_canceled".
func (a *auditPublisher) PostDispatch(_ context.Context, req sextantproto.Envelope, verb, terminal string, durationMs int64, errorCode string) error {
	details := map[string]any{
		"verb":            verb,
		"idempotency_key": derefString(req.IdempotencyKey),
		"terminal_reason": terminal,
		"duration_ms":     durationMs,
	}
	if errorCode != "" {
		details["error_code"] = errorCode
	}
	result := sextantproto.AuditAllowed
	if terminal == "error" {
		result = sextantproto.AuditError
	}
	payload := sextantproto.AuditPayload{
		Actor:              auditActor(req.From),
		Action:             "rpc." + verb + ".result",
		CapabilityRequired: CapFor(verb),
		Result:             result,
		Details:            details,
	}
	env := buildAuditEnvelope(a.from, req, payload)
	return a.publish(subjectAuditRPCResult, env)
}

func (a *auditPublisher) publish(subject string, env sextantproto.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("rpc: marshal audit envelope: %w", err)
	}
	if err := a.nc.Publish(subject, raw); err != nil {
		return fmt.Errorf("rpc: publish %s: %w", subject, err)
	}
	return nil
}

// buildAuditEnvelope constructs a child envelope on the request's trace.
// The audit envelope is a child span — its ParentSpanID points at the
// request's SpanID so a single trace_id query reconstructs the full
// pre/post pair plus the original request.
func buildAuditEnvelope(from sextantproto.Address, req sextantproto.Envelope, payload sextantproto.AuditPayload) sextantproto.Envelope {
	raw, err := json.Marshal(payload)
	if err != nil {
		// Marshal of a fixed-shape struct cannot fail in practice;
		// surface as an empty payload so the envelope still publishes.
		raw = json.RawMessage("{}")
	}
	env := req.Child(sextantproto.KindAudit, from, raw)
	return env
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func resultFor(allowed bool) sextantproto.AuditResult {
	if allowed {
		return sextantproto.AuditAllowed
	}
	return sextantproto.AuditDenied
}

// auditActor projects an envelope's From address into the audit table's
// `actor` column. For agents this is the UUID; for the operator it is
// the literal "operator"; for the daemon it is the daemon ID.
func auditActor(from sextantproto.Address) string {
	if from.ID != "" {
		return from.ID
	}
	return string(from.Kind)
}
