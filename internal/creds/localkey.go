package creds

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// LocalKeyName is the file name of the local client key inside the config
// directory. The key authenticates callers of the loopback proxy; it is
// unrelated to the Qwen subscription credentials.
const LocalKeyName = "client-key"

// EnsureLocalKey returns the local client key, creating it on first use.
func EnsureLocalKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			return key, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, nil
}

// VerifyLocalKey compares a presented bearer token with the stored key in
// constant time.
func VerifyLocalKey(stored, presented string) bool {
	if stored == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(presented)) == 1
}
