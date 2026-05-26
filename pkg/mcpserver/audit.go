package mcpserver

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// auditSubject is the bus subject every tool-call audit envelope lands
// on. Per specs/protocols/bus-subjects.md §audit.
const auditSubject = "audit.tool_call"

// auditPublisher emits one audit envelope per tool invocation
// (allowed | denied | error). Best-effort: a publish failure logs but
// does not propagate — refusing to dispatch because audit publish
// failed would be worse than the missing audit row.
type auditPublisher struct {
	nc     *nats.Conn
	from   sextantproto.Address
	logger *log.Logger
}

// auditEvent is the shape of one in-memory audit decision. The
// publisher converts it into a sextantproto.AuditPayload before
// emitting.
type auditEvent struct {
	Tool       string
	Capability string
	Caller     Caller
	Result     sextantproto.AuditResult
	ErrorCode  string
	DurationMs int64
}

// publish emits ev. Best-effort; a marshal/publish failure logs.
func (a *auditPublisher) publish(ev auditEvent) {
	if a == nil || a.nc == nil {
		return
	}
	details := map[string]any{
		"tool":        ev.Tool,
		"caller_kind": string(ev.Caller.Kind),
		"caller_id":   ev.Caller.ID(),
		"duration_ms": ev.DurationMs,
	}
	if ev.ErrorCode != "" {
		details["error_code"] = ev.ErrorCode
	}
	payload := sextantproto.AuditPayload{
		Actor:              ev.Caller.ID(),
		Action:             "tool_call." + ev.Tool,
		CapabilityRequired: ev.Capability,
		Result:             ev.Result,
		Details:            details,
	}
	if ev.Caller.Kind == CallerAgent && ev.Caller.AgentUUID != uuid.Nil {
		u := ev.Caller.AgentUUID
		payload.AgentUUID = &u
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		a.logf("mcpserver: marshal audit payload: %v", err)
		return
	}
	env := sextantproto.NewEnvelope(sextantproto.KindAudit, a.from, raw)
	body, err := json.Marshal(env)
	if err != nil {
		a.logf("mcpserver: marshal audit envelope: %v", err)
		return
	}
	if err := a.nc.Publish(auditSubject, body); err != nil {
		a.logf("mcpserver: publish %s: %v", auditSubject, err)
	}
}

func (a *auditPublisher) logf(format string, args ...any) {
	if a.logger != nil {
		a.logger.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// auditResultFor maps the dispatcher's terminal state into the
// AuditResult enum. errCode "" → allowed; "capability_denied" → denied;
// anything else → error.
func auditResultFor(errCode string) sextantproto.AuditResult {
	switch errCode {
	case "":
		return sextantproto.AuditAllowed
	case sextantproto.ErrCodeCapabilityDenied:
		return sextantproto.AuditDenied
	default:
		return sextantproto.AuditError
	}
}

// fmtErrf is a tiny helper to keep dispatcher error sites short.
func fmtErrf(code, format string, args ...any) toolError {
	return toolError{Code: code, Message: fmt.Sprintf(format, args...)}
}
