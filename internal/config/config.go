// Package config resolves on-disk locations and process-wide defaults for the
// proxy. Every path can be overridden with QWEN_SUB_PROXY_HOME so the tool can
// run against an isolated profile during tests.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Version is reported in the outbound User-Agent and in `status` output.
const Version = "0.1.0"

// Default bind address. The proxy only ever listens on loopback.
const (
	DefaultHost = "127.0.0.1"
	DefaultPort = 8791
)

// EnvHome overrides the configuration directory.
const EnvHome = "QWEN_SUB_PROXY_HOME"

// EnvClientKey overrides the local client key used to authenticate callers.
const EnvClientKey = "QWEN_SUB_PROXY_KEY"

// Home returns the configuration directory, creating it when needed.
func Home() (string, error) {
	if override := os.Getenv(EnvHome); override != "" {
		return override, os.MkdirAll(override, 0o700)
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, ".config", "qwen-sub-proxy")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// CredsPath returns the path of the stored OAuth credentials.
func CredsPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "credentials.json"), nil
}

// ClientKeyPath returns the path of the local client key.
func ClientKeyPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "client-key"), nil
}

// UserAgent is the honest client identifier sent to Qwen endpoints. The proxy
// deliberately does not impersonate the official Qwen Code client: the
// upstream WAF only rejects the default curl agent, any other value works.
func UserAgent() string {
	return "qwen-sub-proxy/" + Version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}
