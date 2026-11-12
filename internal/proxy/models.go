package proxy

import (
	"net/http"
	"sync"
	"time"

	"qwen-sub-proxy/internal/gateway"
)

// catalogCache stores the gateway model catalog for a limited time and keeps
// the last good copy for stale-on-error responses.
type catalogCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	catalog   *gateway.Catalog
	fetchedAt time.Time
}

func newCatalogCache(ttl time.Duration) *catalogCache {
	return &catalogCache{ttl: ttl}
}

// fresh returns the cached catalog when it is still within the TTL.
func (c *catalogCache) fresh() (*gateway.Catalog, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil, false
	}
	if time.Since(c.fetchedAt) > c.ttl {
		return nil, false
	}
	return c.catalog, true
}

// stale returns the last known catalog regardless of age.
func (c *catalogCache) stale() (*gateway.Catalog, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.catalog, c.fetchedAt
}

// store replaces the cached catalog.
func (c *catalogCache) store(catalog *gateway.Catalog) {
	c.mu.Lock()
	c.catalog = catalog
	c.fetchedAt = time.Now()
	c.mu.Unlock()
}

// handleModels serves the gateway model catalog, preferring the cache.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := http.StatusOK
	defer func() { s.logRequest(r, status, started) }()

	if r.Method != http.MethodGet {
		status = http.StatusMethodNotAllowed
		writeError(w, status, "invalid_request_error", "method_not_allowed", "only GET is supported")
		return
	}

	if catalog, ok := s.catalog.fresh(); ok {
		writeJSON(w, http.StatusOK, catalog)
		return
	}

	token, err := s.opts.Tokens.AccessToken(r.Context())
	if err == nil {
		var catalog *gateway.Catalog
		catalog, err = s.opts.Gateway.Models(r.Context(), token)
		if err == nil {
			s.catalog.store(catalog)
			writeJSON(w, http.StatusOK, catalog)
			return
		}
	}

	if catalog, _ := s.catalog.stale(); catalog != nil {
		writeJSON(w, http.StatusOK, catalog)
		return
	}

	status = http.StatusBadGateway
	writeError(w, status, "upstream_error", "models_unavailable", "cannot fetch the model catalog")
}
