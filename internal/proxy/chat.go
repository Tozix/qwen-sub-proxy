package proxy

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"qwen-sub-proxy/internal/tokenmgr"
)

// handleChat forwards a chat completion request to the Qwen subscription
// gateway. On an upstream 401 the access token is refreshed once and the
// request is replayed exactly once.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := http.StatusOK
	defer func() { s.logRequest(r, status, started) }()

	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		writeError(w, status, "invalid_request_error", "method_not_allowed", "only POST is supported")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
			writeError(w, status, "invalid_request_error", "request_too_large", "request body exceeds the configured limit")
			return
		}
		status = http.StatusBadRequest
		writeError(w, status, "invalid_request_error", "invalid_body", "cannot read request body")
		return
	}

	token, err := s.opts.Tokens.AccessToken(r.Context())
	if err != nil {
		if errors.Is(err, tokenmgr.ErrReauthentication) {
			status = http.StatusUnauthorized
			writeError(w, status, "authentication_error", "reauthentication_required", "run `qwen-sub-proxy login` again")
			return
		}
		status = http.StatusServiceUnavailable
		writeError(w, status, "authentication_error", "token_unavailable", "no usable access token: "+err.Error())
		return
	}

	resp, err := s.opts.Gateway.Do(r.Context(), http.MethodPost, "/chat/completions", token, body)
	if err != nil {
		status = http.StatusBadGateway
		writeError(w, status, "upstream_error", "upstream_unreachable", err.Error())
		return
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		newToken, refreshErr := s.opts.Tokens.ForceRefresh(r.Context())
		if refreshErr != nil {
			if errors.Is(refreshErr, tokenmgr.ErrReauthentication) {
				status = http.StatusUnauthorized
				writeError(w, status, "authentication_error", "reauthentication_required", "run `qwen-sub-proxy login` again")
				return
			}
			status = http.StatusBadGateway
			writeError(w, status, "upstream_error", "token_refresh_failed", refreshErr.Error())
			return
		}
		resp, err = s.opts.Gateway.Do(r.Context(), http.MethodPost, "/chat/completions", newToken, body)
		if err != nil {
			status = http.StatusBadGateway
			writeError(w, status, "upstream_error", "upstream_unreachable", err.Error())
			return
		}
	}
	defer resp.Body.Close()

	status = resp.StatusCode
	copyHeader(w.Header(), resp.Header, "Content-Type")
	copyHeader(w.Header(), resp.Header, "x-request-id")
	copyHeader(w.Header(), resp.Header, "x-gateway-request-id")
	// Rate limit hints have to reach the client, it uses them for backoff.
	copyHeader(w.Header(), resp.Header, "Retry-After")

	if isEventStream(resp.Header.Get("Content-Type")) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(resp.StatusCode)
		streamBody(w, resp.Body)
		return
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// copyHeader copies a single response header from the upstream response.
func copyHeader(dst, src http.Header, name string) {
	if value := src.Get(name); value != "" {
		dst.Set(name, value)
	}
}

// isEventStream reports whether the upstream response is an SSE stream.
func isEventStream(contentType string) bool {
	return strings.HasPrefix(contentType, "text/event-stream")
}
