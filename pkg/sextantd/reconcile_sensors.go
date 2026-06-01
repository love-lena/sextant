package sextantd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// AgentDefinitionsBucket names the NATS KV bucket holding agent records.
// Mirrors pkg/natsboot/layout.go and handlers.AgentDefinitionsBucket;
// declared here so the sextantd package's sensors don't import handlers
// for a constant.
const AgentDefinitionsBucket = "agent_definitions"

// lifecycleSubject is the wildcard the lifecycle hint source subscribes.
const lifecycleSubject = "agents.*.lifecycle"

// LifecycleHintSource subscribes `agents.*.lifecycle` and turns every
// sidecar-driven transition into a reconcile hint (RFC §5.1: L1/L3/
// lifecycle become hint sources that enqueue — they no longer write
// status). It records sidecar-terminal precedence in the reconciler and
// enqueues; the reconciler re-reads desired + re-observes actual and is
// the sole writer of status.
//
// It replaces the old LifecycleWatcher's direct KV writes. The
// incarnation-CAS / stale-envelope discipline is preserved by
// construction: precedence is keyed by incarnation id, and the
// reconciler only consults the precedence flag for the def's CURRENT
// incarnation — a stale incarnation's terminal is recorded under its own
// (old) id and is therefore never consulted.
type LifecycleHintSource struct {
	nc   *nats.Conn
	sink interface {
		OnSidecarLifecycle(sextantproto.LifecyclePayload)
	}

	mu  sync.Mutex
	sub *nats.Subscription
}

// NewLifecycleHintSource subscribes the lifecycle wildcard and returns a
// running hint source. Caller calls Stop() at shutdown.
func NewLifecycleHintSource(nc *nats.Conn, sink interface {
	OnSidecarLifecycle(sextantproto.LifecyclePayload)
},
) (*LifecycleHintSource, error) {
	if nc == nil {
		return nil, fmt.Errorf("lifecycle hint source: nats conn is nil")
	}
	if sink == nil {
		return nil, fmt.Errorf("lifecycle hint source: sink is nil")
	}
	s := &LifecycleHintSource{nc: nc, sink: sink}
	sub, err := nc.Subscribe(lifecycleSubject, s.handle)
	if err != nil {
		return nil, fmt.Errorf("lifecycle hint source: subscribe %s: %w", lifecycleSubject, err)
	}
	s.mu.Lock()
	s.sub = sub
	s.mu.Unlock()
	return s, nil
}

func (s *LifecycleHintSource) handle(msg *nats.Msg) {
	var env sextantproto.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		log.Printf("sextantd: lifecycle hint: decode envelope on %s: %v", msg.Subject, err)
		return
	}
	if env.Kind != sextantproto.KindLifecycle {
		return
	}
	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("sextantd: lifecycle hint: decode payload on %s: %v", msg.Subject, err)
		return
	}
	s.sink.OnSidecarLifecycle(payload)
}

// Stop unsubscribes. Idempotent.
func (s *LifecycleHintSource) Stop() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	sub := s.sub
	s.sub = nil
	s.mu.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		return fmt.Errorf("lifecycle hint source: unsubscribe: %w", err)
	}
	return nil
}

// DieEventSource is the narrow surface the die hint source needs on
// *containermgr.Manager.
type DieEventSource interface {
	Events(ctx context.Context, f containermgr.EventsFilter) (<-chan containermgr.Event, <-chan error)
}

// DieHintSource subscribes docker `die` events for labeled sextant
// sidecars and turns each into a reconcile hint (RFC §5.1: L3 becomes a
// hint source). It calls reconciler.OnDie, which records the die
// timestamp (the 5s debounce anchor) and enqueues — the reconciler does
// NOT mark lost until the debounce elapses without a sidecar terminal.
// It no longer publishes synthetic lost envelopes (the reconciler writes
// status, sole-writer).
type DieHintSource struct {
	src  DieEventSource
	sink interface {
		OnDie(agentID, incarnationID uuid.UUID)
	}
}

// NewDieHintSource constructs a DieHintSource.
func NewDieHintSource(src DieEventSource, sink interface {
	OnDie(agentID, incarnationID uuid.UUID)
},
) *DieHintSource {
	return &DieHintSource{src: src, sink: sink}
}

// Run subscribes container `die` events and blocks until ctx is done.
func (s *DieHintSource) Run(ctx context.Context) error {
	events, errs := s.src.Events(ctx, containermgr.EventsFilter{
		Labels: map[string]string{handlers.LabelAgentUUID: ""},
		Events: []string{"die"},
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if err != nil {
				log.Printf("sextantd: die hint source: events stream: %v", err)
			}
			return err
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			s.handleDie(ev)
		}
	}
}

func (s *DieHintSource) handleDie(ev containermgr.Event) {
	agentStr := ev.Labels[handlers.LabelAgentUUID]
	incStr := ev.Labels[handlers.LabelIncarnationID]
	if agentStr == "" {
		return
	}
	agentID, err := uuid.Parse(agentStr)
	if err != nil {
		return
	}
	incID, _ := uuid.Parse(incStr) // may be Nil; OnDie tolerates it
	s.sink.OnDie(agentID, incID)
}
