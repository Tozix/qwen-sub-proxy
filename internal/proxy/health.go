package proxy

import (
	"net/http"
	"time"

	"qwen-sub-proxy/internal/creds"
)

// healthPayload is the body of GET /health. It never contains tokens.
type healthPayload struct {
	Status         string   `json:"status"`
	Version        string   `json:"version"`
	LoggedIn       bool     `json:"logged_in"`
	MemberType     string   `json:"member_type,omitempty"`
	Email          string   `json:"email,omitempty"`
	TokenExpiresIn int64    `json:"token_expires_in"`
	ReauthRequired bool     `json:"reauth_required"`
	ModelsCachedAt string   `json:"models_cached_at,omitempty"`
	Models         []string `json:"models,omitempty"`
}

// handleHealth reports the state of the proxy and the subscription without
// disclosing any credential material.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := http.StatusOK
	defer func() { s.logRequest(r, status, started) }()

	payload := healthPayload{
		Status:  "ok",
		Version: s.opts.Version,
	}

	if snapshot := s.opts.Tokens.Snapshot(); snapshot != nil {
		payload.LoggedIn = true
		payload.MemberType = snapshot.MemberType
		payload.Email = snapshot.Email
		payload.TokenExpiresIn = int64(creds.ExpiresIn(snapshot).Seconds())
	}
	payload.ReauthRequired = s.opts.Tokens.NeedsReauth()
	if payload.ReauthRequired {
		payload.LoggedIn = false
	}

	if catalog, fetchedAt := s.catalog.stale(); catalog != nil {
		payload.ModelsCachedAt = fetchedAt.UTC().Format(time.RFC3339)
		for _, model := range catalog.Data {
			payload.Models = append(payload.Models, model.ID)
		}
	}

	writeJSON(w, http.StatusOK, payload)
}
