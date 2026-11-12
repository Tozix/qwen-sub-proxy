// Package tokenmgr keeps a usable access token in front of the stored
// credentials and refreshes it in a single-flight fashion, so concurrent
// requests never trigger more than one refresh call.
package tokenmgr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"qwen-sub-proxy/internal/creds"
	"qwen-sub-proxy/internal/oauth"
)

// RefreshLeeway is how long before the actual expiry a token is refreshed.
const RefreshLeeway = 60 * time.Second

// ErrReauthentication is returned when the refresh token was rejected and the
// user has to run `qwen-sub-proxy login` again. The stored credentials are
// kept so the account state stays inspectable.
var ErrReauthentication = errors.New("qwen-sub-proxy: re-authentication required")

// Refresher rotates an OAuth token pair. oauth.Client implements it.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*oauth.Tokens, error)
}

// Manager owns the credential file and the refresh loop.
type Manager struct {
	mu       sync.Mutex
	path     string
	creds    *creds.Credentials
	oauth    Refresher
	leeway   time.Duration
	inflight chan struct{}
	// lastErr holds the error of the last finished refresh so that all
	// waiters of a failed burst observe the same failure instead of each
	// starting a new refresh.
	lastErr error
	// reauthRequired is set when the server rejected the refresh token.
	reauthRequired bool
}

// New loads the stored credentials. It returns creds.ErrNotFound when no
// login has been performed yet.
func New(path string, client Refresher) (*Manager, error) {
	c, err := creds.Load(path)
	if err != nil {
		return nil, err
	}
	return &Manager{path: path, creds: c, oauth: client, leeway: RefreshLeeway}, nil
}

// Snapshot returns a copy of the current credentials.
func (m *Manager) Snapshot() *creds.Credentials {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.creds == nil {
		return nil
	}
	clone := *m.creds
	return &clone
}

// NeedsReauth reports whether the stored credentials were rejected upstream.
func (m *Manager) NeedsReauth() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reauthRequired
}

// AccessToken returns a valid access token, refreshing it when needed.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	return m.obtain(ctx, false)
}

// ForceRefresh rotates the token pair immediately and returns the new access
// token. It is used when the gateway rejects a token with 401.
//
// The rotation runs under the same single-flight guard as a regular refresh:
// concurrent 401 responses must not exchange the same refresh token twice,
// because the first exchange invalidates it.
func (m *Manager) ForceRefresh(ctx context.Context) (string, error) {
	return m.obtain(ctx, true)
}

// obtain returns a usable access token. With force set the token is rotated
// even when it has not expired yet; the rotation itself happens only once
// per burst of concurrent callers.
func (m *Manager) obtain(ctx context.Context, force bool) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		m.mu.Lock()
		if !force && m.creds != nil && !creds.Expired(m.creds, m.leeway) {
			token := m.creds.AccessToken
			m.mu.Unlock()
			return token, nil
		}
		wait := m.inflight
		if wait == nil {
			wait = make(chan struct{})
			m.inflight = wait
			m.mu.Unlock()

			err := m.refresh(ctx)

			m.mu.Lock()
			m.inflight = nil
			m.lastErr = err
			m.mu.Unlock()
			close(wait)

			if err != nil {
				return "", err
			}
			// The token is fresh now, no need to force another rotation.
			force = false
			continue
		}
		m.mu.Unlock()

		select {
		case <-wait:
			m.mu.Lock()
			err := m.lastErr
			m.mu.Unlock()
			if err != nil {
				return "", err
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
		// Somebody else rotated the token, it is fresh now.
		force = false
	}
	return "", errors.New("qwen-sub-proxy: unable to obtain access token")
}

// refresh performs the network refresh and persists the new token pair. The
// caller must not hold the mutex.
func (m *Manager) refresh(ctx context.Context) error {
	m.mu.Lock()
	current := m.creds
	m.mu.Unlock()
	if current == nil || current.RefreshToken == "" {
		return errors.New("qwen-sub-proxy: no refresh token, run `qwen-sub-proxy login`")
	}

	tokens, err := m.oauth.Refresh(ctx, current.RefreshToken)
	if err != nil {
		if oauth.IsInvalidGrant(err) {
			m.mu.Lock()
			m.reauthRequired = true
			m.mu.Unlock()
			return ErrReauthentication
		}
		return fmt.Errorf("qwen-sub-proxy: refresh token: %w", err)
	}
	if tokens.AccessToken == "" {
		return errors.New("qwen-sub-proxy: refresh returned an empty access token")
	}

	// A refresh response may omit the refresh token. Keeping the current one
	// avoids writing an unusable credential file.
	refreshToken := tokens.RefreshToken
	if refreshToken == "" {
		refreshToken = current.RefreshToken
	}

	next := &creds.Credentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: refreshToken,
		TokenType:    tokens.TokenType,
		Scope:        tokens.Scope,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix(),
		Revision:     current.Revision + 1,
		MemberType:   current.MemberType,
		Email:        current.Email,
		Name:         current.Name,
	}
	if err := creds.Save(m.path, next); err != nil {
		return err
	}
	m.mu.Lock()
	m.creds = next
	m.reauthRequired = false
	m.mu.Unlock()
	return nil
}
