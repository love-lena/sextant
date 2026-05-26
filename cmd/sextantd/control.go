package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/templates"
)

// controlRuntime owns the NATS subscriptions for the daemon's lightweight
// control verbs — operator requests that aren't full RPCs but still need
// a request/reply contract. M16 ships one: `templates_reload`. The
// runtime is built once, after the spawn runtime exists (so the templates
// KV handle is available), and torn down in doShutdown.
//
// Wire shape uses native NATS request/reply: the operator publishes a
// request with a NATS-managed reply inbox; the daemon answers via
// msg.Respond. No envelope, no idempotency cache, no audit row — the
// reload is observable through the `audit.kv.put templates/*` envelopes
// SyncDirToKV already triggers.
type controlRuntime struct {
	nc           *nats.Conn
	tplKV        templates.KV
	templatesDir string

	sub *nats.Subscription
}

// startControl subscribes to sextant.control.templates_reload and stamps
// every request with a TemplatesReloadResponse. Returns the runtime;
// daemon shutdown calls stop().
func (d *daemon) startControl(nc *nats.Conn, tplKV templates.KV) (*controlRuntime, error) {
	if nc == nil {
		return nil, fmt.Errorf("control: nats connection is nil")
	}
	if tplKV == nil {
		return nil, fmt.Errorf("control: templates KV is nil")
	}
	rt := &controlRuntime{
		nc:           nc,
		tplKV:        tplKV,
		templatesDir: d.cfg.Paths.TemplatesDir,
	}
	sub, err := nc.Subscribe(sextantd.ControlTemplatesReloadSubject, rt.handleTemplatesReload)
	if err != nil {
		return nil, fmt.Errorf("control: subscribe %s: %w", sextantd.ControlTemplatesReloadSubject, err)
	}
	rt.sub = sub
	return rt, nil
}

// handleTemplatesReload runs templates.SyncDirToKV against the
// daemon-configured templates dir and answers the inbound NATS request
// with a JSON-encoded TemplatesReloadResponse. A reload failure is
// reported in Response.Error so the CLI prints a meaningful message
// instead of timing out.
//
// We cap the sync to 30s — large templates dirs are still small in
// practice (a handful of TOMLs); a hang past that window means
// NATS/JetStream itself is wedged and a no-reply is the correct signal.
func (r *controlRuntime) handleTemplatesReload(msg *nats.Msg) {
	if msg.Reply == "" {
		// No reply inbox — caller does not want an answer. Run the
		// reload anyway (fire-and-forget is a legitimate use case for
		// daemon-internal callers) but log the discarded result.
		log.Printf("sextantd: control.templates_reload received with no reply inbox; running anyway")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := sextantd.ReloadTemplates(ctx, r.tplKV, r.templatesDir)
	resp := sextantd.TemplatesReloadResponse{Count: count}
	if err != nil {
		resp.Error = err.Error()
		log.Printf("sextantd: templates reload from %s failed: %v", r.templatesDir, err)
	} else {
		log.Printf("sextantd: synced %d template(s) from %s into KV (reload)", count, r.templatesDir)
	}
	if msg.Reply == "" {
		return
	}
	raw, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		// json.Marshal of a TemplatesReloadResponse cannot fail in
		// practice (two scalar fields). Defensive: if it ever does we
		// log + drop the reply, and the caller hits its timeout.
		log.Printf("sextantd: marshal templates_reload response: %v", marshalErr)
		return
	}
	if err := msg.Respond(raw); err != nil {
		log.Printf("sextantd: respond on %s: %v", msg.Reply, err)
	}
}

// stop unsubscribes the control subjects. Idempotent.
func (r *controlRuntime) stop() error {
	if r == nil {
		return nil
	}
	if r.sub == nil {
		return nil
	}
	err := r.sub.Unsubscribe()
	r.sub = nil
	if err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		return fmt.Errorf("control: unsubscribe: %w", err)
	}
	return nil
}
