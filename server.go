package memgraphrest

import (
	"log"
	"net/http"
	"sync"

	memgraph "github.com/camggould/memgraph"
)

// StoreKind returns a short label for the store implementation. Used by
// /v1/info. Callers pass whatever they want; defaults to "memgraph".
type Option func(*Server)

// Server wraps a memgraph.Store and exposes it over HTTP+SSE.
// Server does NOT own the store; the caller is responsible for closing it.
type Server struct {
	store     memgraph.Store
	logger    *log.Logger
	token     string
	cors      []string
	version   string
	storeKind string

	mu      sync.Mutex
	handler http.Handler

	// SSE bookkeeping
	subsMu      sync.Mutex
	sseSubs     map[*sseClient]struct{}
	sseUnsub    memgraph.Unsubscribe
	sseStartErr error
}

// WithToken enables bearer-token auth on every non-health endpoint. Empty
// token disables auth.
func WithToken(t string) Option {
	return func(s *Server) { s.token = t }
}

// WithCORS enables CORS for the given origins. If origins is empty, CORS
// is disabled.
func WithCORS(origins ...string) Option {
	return func(s *Server) { s.cors = origins }
}

// WithLogger sets the logger used for request logging and panics.
func WithLogger(l *log.Logger) Option {
	return func(s *Server) { s.logger = l }
}

// WithVersion sets the version string reported by /v1/info.
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithStoreKind sets the store label reported by /v1/info (e.g. "sqlite").
func WithStoreKind(k string) Option {
	return func(s *Server) { s.storeKind = k }
}

// New constructs a Server. The mux is built lazily on the first call to
// Handler so options may be applied freely.
func New(store memgraph.Store, opts ...Option) *Server {
	s := &Server{
		store:     store,
		logger:    log.Default(),
		version:   "dev",
		storeKind: "memgraph",
		sseSubs:   make(map[*sseClient]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AuthEnabled reports whether bearer-token auth is enabled.
func (s *Server) AuthEnabled() bool { return s.token != "" }

// CORSEnabled reports whether CORS is enabled.
func (s *Server) CORSEnabled() bool { return len(s.cors) > 0 }

// Handler builds and returns the wired mux+middleware. Idempotent.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handler != nil {
		return s.handler
	}
	mux := http.NewServeMux()
	s.routes(mux)

	var h http.Handler = mux
	h = corsMiddleware(s.cors)(h)
	h = authMiddleware(s.token, s.logger)(h)
	h = loggingMiddleware(s.logger)(h)
	h = recoveryMiddleware(s.logger)(h)
	s.handler = h
	return h
}

// Close unsubscribes any live SSE subscription and disconnects active SSE
// clients. The underlying store is NOT closed (the caller owns it).
func (s *Server) Close() error {
	s.subsMu.Lock()
	if s.sseUnsub != nil {
		s.sseUnsub()
		s.sseUnsub = nil
	}
	for c := range s.sseSubs {
		close(c.done)
	}
	s.sseSubs = make(map[*sseClient]struct{})
	s.subsMu.Unlock()
	return nil
}
