// Package proxy implements the loopback HTTP surface of qwen-sub-proxy: an
// OpenAI-compatible endpoint that forwards requests to the Qwen subscription
// gateway using the OAuth credentials of the logged-in account.
package proxy

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"qwen-sub-proxy/internal/creds"
	"qwen-sub-proxy/internal/gateway"
)

// TokenProvider supplies access tokens and can rotate them on demand.
type TokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context) (string, error)
	Snapshot() *creds.Credentials
	NeedsReauth() bool
}

// Options configures the proxy server.
type Options struct {
	// ClientKey authenticates inbound requests from the local client.
	ClientKey string
	// Tokens provides the upstream bearer token.
	Tokens TokenProvider
	// Gateway talks to the Qwen subscription gateway.
	Gateway *gateway.Client
	// Logger receives request logs. Tokens are never logged.
	Logger *log.Logger
	// MaxBodyBytes bounds the size of an inbound request body.
	MaxBodyBytes int64
	// ModelCacheTTL is how long the gateway model catalog is cached.
	ModelCacheTTL time.Duration
	// Version is reported by the health endpoint.
	Version string
}

// Defaults used when Options leaves a value empty.
const (
	DefaultMaxBodyBytes  = 32 << 20
	DefaultModelCacheTTL = 10 * time.Minute
)

// Server routes inbound requests.
type Server struct {
	opts    Options
	logger  *log.Logger
	catalog *catalogCache
}

// New builds a Server.
func New(opts Options) *Server {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.ModelCacheTTL <= 0 {
		opts.ModelCacheTTL = DefaultModelCacheTTL
	}
	if opts.Logger == nil {
		opts.Logger = log.New(log.Writer(), "", log.LstdFlags)
	}
	return &Server{
		opts:    opts,
		logger:  opts.Logger,
		catalog: newCatalogCache(opts.ModelCacheTTL),
	}
}

// Handler returns the HTTP handler of the proxy.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/models/", s.handleModels)
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/v1/chat/completions/", s.handleChat)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "unknown route")
	})
	return s.recoverer(s.loopbackOnly(s.authenticate(mux)))
}

// recoverer turns a panic into a 500 instead of killing the process.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Printf("panic on %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "internal_error", "proxy_panic", "internal proxy error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// loopbackOnly rejects requests that did not come from the loopback interface.
func (s *Server) loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
			writeError(w, http.StatusForbidden, "permission_error", "loopback_only", "only loopback clients are allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate enforces the local bearer key.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := ""
		if header := r.Header.Get("Authorization"); header != "" {
			parts := strings.SplitN(header, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				presented = strings.TrimSpace(parts[1])
			}
		}
		if !creds.VerifyLocalKey(s.opts.ClientKey, presented) {
			writeError(w, http.StatusUnauthorized, "authentication_error", "invalid_local_key", "invalid local API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// logRequest writes a one line access log entry.
func (s *Server) logRequest(r *http.Request, status int, started time.Time) {
	s.logger.Printf("%s %s -> %d in %s", r.Method, r.URL.Path, status, time.Since(started).Round(time.Millisecond))
}
