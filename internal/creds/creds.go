// Package creds stores the OAuth credentials obtained from the Qwen
// subscription in a private file with 0600 permissions.
package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credentials is the persisted OAuth state of a chat.qwen.ai subscription.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	// ExpiresAt is a unix timestamp in seconds.
	ExpiresAt  int64  `json:"expires_at"`
	MemberType string `json:"member_type,omitempty"`
	Email      string `json:"email,omitempty"`
	Name       string `json:"name,omitempty"`
	// Revision increments on every successful refresh so a stale in-memory
	// copy can be detected.
	Revision  int64 `json:"revision"`
	UpdatedAt int64 `json:"updated_at"`
}

// ErrNotFound is returned when no credentials have been stored yet.
var ErrNotFound = errors.New("qwen-sub-proxy: no credentials stored")

// Load reads the credentials file.
func Load(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("qwen-sub-proxy: parse credentials: %w", err)
	}
	return &c, nil
}

// Save writes the credentials atomically with 0600 permissions. The parent
// directory is created with 0700.
func Save(path string, c *Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Unix()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Flush the content before the rename so a crash cannot leave a renamed
	// but empty credential file.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Delete removes the credentials file. A missing file is not an error.
func Delete(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Expired reports whether the access token is expired, treating a token that
// expires within leeway as expired so it is refreshed before it is used.
func Expired(c *Credentials, leeway time.Duration) bool {
	if c == nil || c.AccessToken == "" {
		return true
	}
	if c.ExpiresAt == 0 {
		return false
	}
	return time.Now().Add(leeway).Unix() >= c.ExpiresAt
}

// ExpiresIn returns the remaining lifetime of the access token.
func ExpiresIn(c *Credentials) time.Duration {
	if c == nil || c.ExpiresAt == 0 {
		return 0
	}
	return time.Until(time.Unix(c.ExpiresAt, 0))
}
