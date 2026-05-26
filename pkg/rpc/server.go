package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// subjectPrefix matches the wildcard "sextant.rpc.*" subscription. The
// trailing token is the verb.
const subjectPrefix = "sextant.rpc."

// Default knobs.
const (
	defaultHandlerTimeout = 10 * time.Second
	defaultPruneInterval  = 30 * time.Second
)

// Handler runs a single RPC. ctx carries the per-request deadline (the
// shorter of the server's default handler timeout and any deadline
// inherited from the request envelope). req is the inbound envelope;
// the handler must call emit at least once with Terminal: true to fulfil
// the wire contract. Returning a non-nil error after emit() has already
// fired a terminal response is a bug — the server logs and drops the
// late error to keep replies single-terminal.
//
// Streaming handlers may call emit multiple times with Terminal: false
// before a final Terminal: true call. M7 ships no streaming verbs.
type Handler func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error

// Config bundles the knobs the Server's constructor takes. Zero-value
// fields fall back to sane defaults so the daemon can hand New a bare
// Config in production.
type Config struct {
	// From is the address the server stamps into outbound audit and
	// reply envelopes. The daemon uses an "operator" or "daemon-<host>"
	// address (architecture.md §10b).
	From sextantproto.Address

	// HandlerTimeout caps how long a single handler may run. Default
	// 10s — matches the client-side default request timeout so the
	// server emits a timeout reply at roughly the same instant the
	// caller unsubscribes.
	HandlerTimeout time.Duration

	// IdempotencyTTL is the (verb, idempotency_key) cache window.
	// Default 60s per spec.
	IdempotencyTTL time.Duration

	// CapChecker decides whether a request is permitted. Defaults to
	// AllowAll{} (the M7 operator-path stub).
	CapChecker CapabilityChecker

	// Now is the time source the idempotency cache uses; defaults to
	// time.Now. Injectable for tests.
	Now func() time.Time

	// Logger receives diagnostic messages. Defaults to the stdlib log
	// package's default.
	Logger *log.Logger
}

// Server owns the NATS subscription that fans incoming RPC envelopes out
// to registered handlers. One Server per NATS connection.
//
// Build with New, register verbs via Register, then call Run. Stop with
// Close. Server is safe for concurrent Register/Close.
type Server struct {
	nc       *nats.Conn
	cfg      Config
	audit    *auditPublisher
	idem     *idemCache
	logger   *log.Logger
	ttlPrune time.Duration

	mu       sync.RWMutex
	handlers map[string]Handler
	caps     map[string]string
	closed   bool

	sub   *nats.Subscription
	runMu sync.Mutex // serializes Run vs Close

	wg sync.WaitGroup // tracks in-flight dispatches
}

// New builds a Server bound to nc. The Server does not subscribe until
// Run is called.
func New(nc *nats.Conn, cfg Config) (*Server, error) {
	if nc == nil {
		return nil, fmt.Errorf("rpc: nats connection is nil")
	}
	if cfg.From.Kind == "" {
		return nil, fmt.Errorf("rpc: Config.From.Kind is required")
	}
	if cfg.HandlerTimeout <= 0 {
		cfg.HandlerTimeout = defaultHandlerTimeout
	}
	if cfg.IdempotencyTTL <= 0 {
		cfg.IdempotencyTTL = idempotencyTTL
	}
	if cfg.CapChecker == nil {
		cfg.CapChecker = AllowAll{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Server{
		nc:       nc,
		cfg:      cfg,
		audit:    newAuditPublisher(nc, cfg.From),
		idem:     newIdemCache(cfg.Now, cfg.IdempotencyTTL),
		logger:   cfg.Logger,
		handlers: make(map[string]Handler),
		caps:     make(map[string]string),
		ttlPrune: defaultPruneInterval,
	}, nil
}

// Register installs a handler for verb. Calling Register after Run is
// safe but discouraged: incoming requests for a verb registered after
// Run started may race against the first batch of dispatches. The
// daemon registers everything before calling Run.
//
// Re-registering an existing verb returns an error rather than silently
// replacing — accidental shadowing has burned other servers we want to
// avoid.
func (s *Server) Register(verb string, h Handler) error {
	if verb == "" {
		return fmt.Errorf("rpc: Register: verb is empty")
	}
	if h == nil {
		return fmt.Errorf("rpc: Register: handler for %q is nil", verb)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.handlers[verb]; dup {
		return fmt.Errorf("rpc: Register: verb %q already registered", verb)
	}
	s.handlers[verb] = h
	s.caps[verb] = CapFor(verb)
	return nil
}

// Run subscribes to "sextant.rpc.*" and blocks until ctx is canceled
// or Close is called. The subscription dispatches each incoming
// envelope on its own goroutine so a slow handler does not block the
// next request.
//
// Run returns nil on clean shutdown; the only error path is the
// subscribe call itself.
func (s *Server) Run(ctx context.Context) error {
	s.runMu.Lock()
	if s.closed {
		s.runMu.Unlock()
		return fmt.Errorf("rpc: Run on closed server")
	}
	sub, err := s.nc.Subscribe(subjectPrefix+"*", func(msg *nats.Msg) {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, msg)
		}()
	})
	if err != nil {
		s.runMu.Unlock()
		return fmt.Errorf("rpc: subscribe %s*: %w", subjectPrefix, err)
	}
	s.sub = sub
	s.runMu.Unlock()

	// Background idempotency-cache pruner. Exits when ctx is canceled.
	pruneDone := make(chan struct{})
	go s.prune(ctx, pruneDone)

	<-ctx.Done()
	<-pruneDone
	return nil
}

// Close unsubscribes and waits for in-flight dispatches to drain. After
// Close returns, no more handlers will start. Idempotent.
func (s *Server) Close() error {
	s.runMu.Lock()
	if s.closed {
		s.runMu.Unlock()
		return nil
	}
	s.closed = true
	sub := s.sub
	s.sub = nil
	s.runMu.Unlock()

	var unsubErr error
	if sub != nil {
		if err := sub.Unsubscribe(); err != nil {
			unsubErr = fmt.Errorf("rpc: unsubscribe: %w", err)
		}
	}
	s.wg.Wait()
	return unsubErr
}

// prune ticks the idempotency cache sweeper.
func (s *Server) prune(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(s.ttlPrune)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.idem.Sweep()
		}
	}
}

// handle is the per-message dispatch. It validates the inbound
// envelope, runs cap check + idempotency + audit, executes the handler
// with a per-request emit callback, and ensures exactly one terminal
// reply is published.
//
// runCtx is the server's Run context — handler execution is rooted in
// it so a daemon shutdown cancels in-flight handlers cleanly.
//
// All error paths emit a terminal RPCError reply — a missing reply is
// a protocol violation per spec.
func (s *Server) handle(runCtx context.Context, msg *nats.Msg) {
	var req sextantproto.Envelope
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		s.logger.Printf("rpc: bad envelope on %s: %v", msg.Subject, err)
		s.replyErrorRaw(msg.Reply, req, sextantproto.ErrCodeBadRequest,
			fmt.Sprintf("malformed envelope: %v", err))
		return
	}
	if err := req.Validate(); err != nil {
		s.logger.Printf("rpc: invalid envelope on %s: %v", msg.Subject, err)
		s.replyErrorRaw(msg.Reply, req, sextantproto.ErrCodeBadRequest,
			fmt.Sprintf("envelope failed validation: %v", err))
		return
	}
	if req.Kind != sextantproto.KindRPCRequest {
		s.replyErrorRaw(msg.Reply, req, sextantproto.ErrCodeBadRequest,
			fmt.Sprintf("envelope kind %q; want %q", req.Kind, sextantproto.KindRPCRequest))
		return
	}
	reply := derefString(req.ReplyTo)
	if reply == "" && msg.Reply != "" {
		// Tolerate clients that forgot to set ReplyTo but published
		// with a NATS Reply header — most NATS request paths do this
		// for us. We still prefer Envelope.ReplyTo.
		reply = msg.Reply
	}
	if reply == "" {
		s.logger.Printf("rpc: %s missing reply_to; dropping (cannot reply)", msg.Subject)
		return
	}
	verb := strings.TrimPrefix(msg.Subject, subjectPrefix)
	if verb == "" || strings.Contains(verb, ".") {
		s.replyErrorTo(reply, req, sextantproto.ErrCodeUnknownVerb,
			fmt.Sprintf("invalid verb subject %q", msg.Subject), verb)
		return
	}
	idemKey := derefString(req.IdempotencyKey)
	if idemKey == "" {
		s.replyErrorTo(reply, req, sextantproto.ErrCodeBadRequest,
			"idempotency_key is required", verb)
		return
	}

	s.mu.RLock()
	h, ok := s.handlers[verb]
	requiredCap := s.caps[verb]
	s.mu.RUnlock()
	if !ok {
		_ = s.audit.PreDispatch(runCtx, req, verb, requiredCap, false)
		s.replyErrorTo(reply, req, sextantproto.ErrCodeUnknownVerb,
			fmt.Sprintf("no handler for %q", verb), verb)
		_ = s.audit.PostDispatch(runCtx, req, verb, "error", 0, sextantproto.ErrCodeUnknownVerb)
		return
	}

	// Idempotency replay: serve the cached reply byte-for-byte. No
	// handler re-execution, no second audit pair — the spec's audit
	// rules are "once per request" and a replay counts as the same
	// request.
	if cached, hit := s.idem.Lookup(verb, idemKey); hit {
		if err := s.nc.Publish(reply, cached); err != nil {
			s.logger.Printf("rpc: publish cached reply on %s: %v", reply, err)
		}
		return
	}

	if err := s.cfg.CapChecker.Check(req, requiredCap); err != nil {
		_ = s.audit.PreDispatch(runCtx, req, verb, requiredCap, false)
		// Surface the cap name in Details so M10 operator tooling can
		// render "missing capability X" without parsing the message.
		s.replyErrorToWithDetails(reply, req, sextantproto.ErrCodeCapabilityDenied,
			err.Error(), verb,
			map[string]any{"capability_required": requiredCap})
		_ = s.audit.PostDispatch(runCtx, req, verb, "error", 0, sextantproto.ErrCodeCapabilityDenied)
		return
	}

	if err := s.audit.PreDispatch(runCtx, req, verb, requiredCap, true); err != nil {
		s.logger.Printf("rpc: audit.PreDispatch: %v", err)
	}

	ctx, cancel := context.WithTimeout(runCtx, s.cfg.HandlerTimeout)
	defer cancel()

	start := s.cfg.Now()
	var (
		emitMu       sync.Mutex
		terminalSent bool
		terminalCode string // empty == success
		emitErr      error
	)
	emit := func(resp sextantproto.RPCResponse) {
		emitMu.Lock()
		defer emitMu.Unlock()
		if terminalSent {
			// Drop late emits — see Handler doc.
			s.logger.Printf("rpc: %s emit after terminal; dropping", verb)
			return
		}
		envBytes, err := buildResponseBytes(s.cfg.From, req, resp)
		if err != nil {
			s.logger.Printf("rpc: build response: %v", err)
			emitErr = err
			return
		}
		if err := s.nc.Publish(reply, envBytes); err != nil {
			s.logger.Printf("rpc: publish reply on %s: %v", reply, err)
			emitErr = err
			return
		}
		if resp.Terminal {
			terminalSent = true
			if resp.Error != nil {
				terminalCode = resp.Error.Code
			}
			if idemKey != "" {
				s.idem.Store(verb, idemKey, envBytes)
			}
		}
	}

	panicked, handlerErr := s.runHandler(ctx, h, req, emit, verb)
	emitMu.Lock()
	sent := terminalSent
	emitMu.Unlock()

	// Handler returned without emitting a terminal response — we must
	// supply one. A non-nil handler error (or a panic) becomes the
	// reply; a clean exit without a reply is a handler bug; we reply
	// with internal so the caller doesn't hang.
	if !sent {
		code := sextantproto.ErrCodeInternal
		msgText := "handler exited without sending a terminal reply"
		var details map[string]any
		switch {
		case panicked != nil:
			// Panic: surface as ErrCodeInternal with a description of
			// the panic in Details so the caller (and the audit row)
			// have something to grep for.
			code = sextantproto.ErrCodeInternal
			msgText = fmt.Sprintf("handler panic: %v", panicked)
			details = map[string]any{
				"panic": fmt.Sprintf("%v", panicked),
				"verb":  verb,
			}
		case handlerErr != nil:
			if errors.Is(handlerErr, context.DeadlineExceeded) {
				code = sextantproto.ErrCodeTimeout
				msgText = "handler timed out"
			} else {
				msgText = handlerErr.Error()
			}
		}
		s.replyErrorToWithDetails(reply, req, code, msgText, verb, details)
		emitMu.Lock()
		terminalCode = code
		emitMu.Unlock()
	} else if emitErr != nil {
		// Best-effort log; the terminal reply is already on the wire.
		s.logger.Printf("rpc: %s emit error: %v", verb, emitErr)
	}

	durMs := s.cfg.Now().Sub(start).Milliseconds()
	terminalReason := "success"
	if terminalCode != "" {
		terminalReason = "error"
	}
	if err := s.audit.PostDispatch(runCtx, req, verb, terminalReason, durMs, terminalCode); err != nil {
		s.logger.Printf("rpc: audit.PostDispatch: %v", err)
	}
}

// runHandler invokes h under a recovery so a handler panic becomes a
// structured terminal reply rather than killing the daemon. Returns
// the recovered panic value (nil if the handler exited normally) and
// the handler's error (if any). The dispatch loop in handle uses both
// signals to choose the right terminal RPCError code.
//
// The wg.Done() that pairs with the dispatch goroutine's wg.Add(1) is
// still in the outer goroutine — Close()'s drain waits on it regardless
// of whether the handler panicked.
func (s *Server) runHandler(ctx context.Context, h Handler, req sextantproto.Envelope, emit func(sextantproto.RPCResponse), verb string) (panicked any, handlerErr error) {
	defer func() {
		if r := recover(); r != nil {
			panicked = r
			s.logger.Printf("rpc: handler panic in verb %q: %v\n%s",
				verb, r, debug.Stack())
		}
	}()
	handlerErr = h(ctx, req, emit)
	return nil, handlerErr
}

// replyErrorTo publishes a terminal RPC error reply on the supplied
// subject. Also caches the reply under the request's idempotency key so
// retries collapse onto the same error.
func (s *Server) replyErrorTo(reply string, req sextantproto.Envelope, code, message, verb string) {
	s.replyErrorToWithDetails(reply, req, code, message, verb, nil)
}

// replyErrorToWithDetails is replyErrorTo with the RPCError.Details
// field populated. Used by paths that want to surface structured
// metadata alongside the error (e.g. capability_required on a
// capability_denied error so M10 operator tooling can render
// "missing capability X" without parsing the message string).
func (s *Server) replyErrorToWithDetails(reply string, req sextantproto.Envelope, code, message, verb string, details map[string]any) {
	rerr := &sextantproto.RPCError{Code: code, Message: message}
	if len(details) > 0 {
		rerr.Details = details
	}
	resp := sextantproto.RPCResponse{
		Error:    rerr,
		Terminal: true,
	}
	envBytes, err := buildResponseBytes(s.cfg.From, req, resp)
	if err != nil {
		s.logger.Printf("rpc: build error reply: %v", err)
		return
	}
	if err := s.nc.Publish(reply, envBytes); err != nil {
		s.logger.Printf("rpc: publish error reply on %s: %v", reply, err)
		return
	}
	if k := derefString(req.IdempotencyKey); k != "" && verb != "" {
		s.idem.Store(verb, k, envBytes)
	}
}

// replyErrorRaw publishes an error reply when the request envelope
// failed pre-validation and we cannot rely on Envelope.ReplyTo. Falls
// back to a NATS-supplied reply subject; if neither is set we cannot
// reply and the caller will hit its timeout — that's spec.
func (s *Server) replyErrorRaw(natsReply string, req sextantproto.Envelope, code, message string) {
	reply := derefString(req.ReplyTo)
	if reply == "" {
		reply = natsReply
	}
	if reply == "" {
		return
	}
	s.replyErrorTo(reply, req, code, message, "")
}

// buildResponseBytes wraps an RPCResponse in an rpc_response envelope
// and returns the marshalled bytes ready for nats.Publish.
func buildResponseBytes(from sextantproto.Address, req sextantproto.Envelope, resp sextantproto.RPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal RPCResponse: %w", err)
	}
	env := req.Child(sextantproto.KindRPCResponse, from, payload)
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal response envelope: %w", err)
	}
	return out, nil
}
