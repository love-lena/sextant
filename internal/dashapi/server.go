// Package dashapi is the dash's local HTTP API face (D1, TASK-68): a stable,
// token-gated REST + SSE surface served on 127.0.0.1, behind which the Go
// process stays the SINGLE bus client. A browser (the zero-design debug surface,
// or a later designed UI) talks only to this local API and never touches the
// bus directly, so bus credentials stay inside the process. The API contract is
// the deliberate, stable boundary; the UI is a swappable face on it.
//
// The server depends only on Bus — the narrow subset of *sextant.Client it
// needs — so the handlers are exercised against a fake without a live bus.
package dashapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
)

// Bus is the subset of *sextant.Client the API server reads from and writes to.
// *sextant.Client satisfies it; tests supply a fake. Keeping the dependency
// narrow is what makes the handlers testable without standing up a bus.
type Bus interface {
	ID() string
	DisplayName() string
	Principal() string
	ListClients(ctx context.Context) ([]sextant.ClientInfo, error)
	FetchMessages(ctx context.Context, subject string, since uint64, limit int) ([]wire.Frame, uint64, error)
	ListArtifacts(ctx context.Context) ([]sextant.ArtifactInfo, error)
	GetArtifact(ctx context.Context, name string) (sextant.Artifact, error)
	// UpdateArtifact compare-and-sets an artifact's record (expectedRev guards a
	// concurrent write); the dash uses it to persist the review-state convention.
	UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error)
	Publish(ctx context.Context, subject string, record json.RawMessage) error
	Subscribe(ctx context.Context, subject string, h sextant.Handler, opts ...sextant.SubOption) (sextant.Subscription, error)
}

// Config configures a Server.
type Config struct {
	// Bus is the connected bus client the API reads/writes through. Required.
	Bus Bus
	// Token is the per-launch secret every /api request must present (Bearer
	// header or ?token=). Required; an empty token rejects all /api requests.
	Token string
	// AllowedOrigins are extra browser origins permitted beyond localhost
	// (127.0.0.1 and localhost on any port are always allowed). Used for a
	// separate dev server hosting the UI during D2 work.
	AllowedOrigins []string
	// UIDir, when set, serves a custom frontend directory at / instead of the
	// built-in zero-design debug surface (the D2 hook).
	UIDir string
}

// Server is the local HTTP API + debug surface. It implements http.Handler.
type Server struct {
	bus            Bus
	token          string
	allowedOrigins []string
	uiDir          string
	mux            *http.ServeMux

	subjMu   sync.Mutex
	subjects map[string]uint64 // subjects seen via Watch (msg.>), for /api/subjects
}

// New builds a Server from cfg. The returned Server is an http.Handler ready to
// pass to http.Serve.
func New(cfg Config) *Server {
	s := &Server{
		bus:            cfg.Bus,
		token:          cfg.Token,
		allowedOrigins: cfg.AllowedOrigins,
		uiDir:          cfg.UIDir,
		mux:            http.NewServeMux(),
		subjects:       map[string]uint64{},
	}
	s.routes()
	return s
}

// routes registers the API routes. Each /api route is wrapped by the token gate.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/self", s.gate(s.handleSelf))
	s.mux.HandleFunc("GET /api/clients", s.gate(s.handleClients))
	s.mux.HandleFunc("GET /api/messages", s.gate(s.handleMessages))
	s.mux.HandleFunc("GET /api/artifacts", s.gate(s.handleArtifacts))
	s.mux.HandleFunc("GET /api/subjects", s.gate(s.handleSubjects))
	s.mux.HandleFunc("GET /api/artifacts/{name}", s.gate(s.handleArtifactGet))
	s.mux.HandleFunc("POST /api/artifacts/{name}/review", s.gate(s.handleArtifactReview))
	s.mux.HandleFunc("POST /api/publish", s.gate(s.handlePublish))
	s.mux.HandleFunc("GET /api/stream", s.gate(s.handleStream))
	s.mux.HandleFunc("GET /debug", s.handleDebug)
	s.mux.HandleFunc("GET /", s.handleRoot)
}

// ServeHTTP applies the origin guard to /api requests, then dispatches to the
// registered routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.applyCORS(w, r) {
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// applyCORS enforces the allowed-origin policy for a browser request and answers
// CORS preflight. It returns false when it has already written the response
// (a rejected origin, or a handled OPTIONS preflight) and the caller must stop.
// A request with no Origin header (curl, a same-origin simple GET) is not
// origin-gated — only the token gate applies.
func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !s.originAllowed(origin) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// originAllowed reports whether a browser Origin may call the API: any localhost
// origin (127.0.0.1, ::1, or the hostname "localhost", on any port) is always
// allowed — that is the same-origin debug surface and a local dev server — plus
// any origin in the configured allow-list (exact match). A malformed origin is
// rejected.
func (s *Server) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	for _, allowed := range s.allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// gate wraps an /api handler with the access check (see authorized): a loopback
// peer is allowed without a token (ADR-0032 loopback exception, TASK-115); any
// non-loopback peer must present the per-launch token as `Authorization: Bearer
// <token>` or `?token=<token>`, or it is rejected 401.
func (s *Server) gate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "missing or invalid token")
			return
		}
		h(w, r)
	}
}

// authorized reports whether r may access the API. A loopback peer (127.0.0.0/8
// or ::1) is authorized WITHOUT a token: the dash listener is loopback-bound and
// loopback is host-bound + implicitly trusted (standard localhost practice, cf.
// OAuth's native-app loopback redirect), so the token's CSRF/remote barrier adds
// nothing there (ADR-0032 loopback exception, TASK-115). Any non-loopback peer
// must carry the valid per-launch token, as a `Authorization: Bearer <token>`
// header or a `?token=` query value; an empty server token is never authorized.
// The comparison is constant-time so a token can't be recovered by timing.
func (s *Server) authorized(r *http.Request) bool {
	if isLoopback(r.RemoteAddr) {
		return true
	}
	if s.token == "" {
		return false
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tokenEqual(bearer, s.token) {
		return true
	}
	return tokenEqual(r.URL.Query().Get("token"), s.token)
}

// isLoopback reports whether a request's peer address is a loopback IP
// (127.0.0.0/8 or ::1). A malformed/empty address is treated as non-loopback, so
// an unparseable peer falls through to the token check (deny-by-default).
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tokenEqual compares two tokens in constant time (subtle.ConstantTimeCompare
// returns 0 for unequal lengths, which only reveals length — fine for a
// fixed-size token).
func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// handleSelf reports who this dash is on the bus and the current principal — the
// header context a UI shows ("you are X; the principal is Y").
func (s *Server) handleSelf(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, selfResponse{
		ID:          s.bus.ID(),
		DisplayName: s.bus.DisplayName(),
		Principal:   s.bus.Principal(),
	})
}

type selfResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Principal   string `json:"principal"`
}

// handleClients returns the clients directory — JSON parity with
// `sextant clients list --json` (the same []sextant.ClientInfo).
func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.bus.ListClients(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

// handleArtifacts returns the artifacts directory — JSON parity with
// `sextant artifact list --json` (the same []sextant.ArtifactInfo).
func (s *Server) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := s.bus.ListArtifacts(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

// handleArtifactGet returns one artifact by name — JSON parity with
// `sextant artifact get --json`. A failed get is reported 404: the page only
// gets names it just listed, so the realistic failure on a valid name is that it
// was deleted in between; a bus-down failure surfaces first on the list
// endpoints the page loads. (D1 keeps a coarse taxonomy here by design.)
func (s *Server) handleArtifactGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	art, err := s.bus.GetArtifact(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, art)
}

// messagesResponse wraps a read batch: the frames and the cursor to resume from
// (pass it back as ?since= for the next page — no gaps, no duplicates).
type messagesResponse struct {
	Messages   []wire.Frame `json:"messages"`
	NextCursor uint64       `json:"next_cursor"`
}

// handleMessages reads a batch of retained messages on a subject — JSON parity
// with `sextant read` (the frames are the same wire.Frame). subject is required;
// since (cursor, default 0) and limit (default 100, matching the CLI) are
// optional.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	if subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	since, err := parseUint(r.URL.Query().Get("since"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "since must be a non-negative integer")
		return
	}
	limit, err := parseInt(r.URL.Query().Get("limit"), 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "limit must be an integer")
		return
	}
	frames, next, err := s.bus.FetchMessages(r.Context(), subject, since, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messagesResponse{Messages: frames, NextCursor: next})
}

// publishRequest is the POST /api/publish body: a subject and the opaque lexicon
// record to publish on it.
type publishRequest struct {
	Subject string          `json:"subject"`
	Record  json.RawMessage `json:"record"`
}

// handlePublish publishes a record on a subject — the "commands" half of the
// API. It validates the request shape, then forwards to the bus; the bus owns
// frame stamping and the subject-space check. A bus refusal is reported 502.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON {subject, record}")
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	if len(req.Record) == 0 || !json.Valid(req.Record) {
		writeError(w, http.StatusBadRequest, "record must be a non-empty JSON value")
		return
	}
	if err := s.bus.Publish(r.Context(), req.Subject, req.Record); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"published": req.Subject})
}

// streamEvent is one Server-Sent Event on the live stream: a frame the bus
// relayed on the subscribed subject, with the bus sequence for ordering.
type streamEvent struct {
	Subject  string     `json:"subject"`
	Sequence uint64     `json:"sequence"`
	Frame    wire.Frame `json:"frame"`
}

// handleStream is the live push (Server-Sent Events): it subscribes to subject
// and writes each delivered frame as a `data:` event until the client
// disconnects (the request context is cancelled), then stops the subscription so
// no bus relay outlives the closed connection. The subscribe handler hands
// frames off on a buffered channel rather than writing the response directly, so
// only this goroutine ever touches the ResponseWriter; a slow consumer drops
// frames rather than blocking the SDK's delivery goroutine.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	subject := r.URL.Query().Get("subject")
	if subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	frames := make(chan sextant.Message, 64)
	sub, err := s.bus.Subscribe(r.Context(), subject, func(m sextant.Message) {
		select {
		case frames <- m:
		default: // slow consumer: drop rather than stall the SDK delivery goroutine
		}
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer sub.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// An opening comment flushes headers so the client sees the stream open
	// before the first frame arrives.
	fmt.Fprint(w, ": stream open\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-frames:
			b, err := json.Marshal(streamEvent{Subject: m.Subject, Sequence: m.Sequence, Frame: m.Frame})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

// parseUint parses a uint64 query value, returning def when empty.
func parseUint(s string, def uint64) (uint64, error) {
	if s == "" {
		return def, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

// parseInt parses an int query value, returning def when empty.
func parseInt(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

// writeJSON writes v as an indented JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeError writes a JSON error envelope: {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
