package creds

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveUsesRestrictivePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(dir, "credentials.json")

	if err := Save(path, &Credentials{AccessToken: "a", RefreshToken: "b"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("directory permissions = %o, want 700", perm)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file permissions = %o, want 600", perm)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	want := &Credentials{AccessToken: "a", RefreshToken: "b", MemberType: "pro", ExpiresAt: 42}
	if err := Save(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.MemberType != want.MemberType {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEnsureLocalKeyIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client-key")
	first, err := EnsureLocalKey(path)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	second, err := EnsureLocalKey(path)
	if err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if first != second {
		t.Fatalf("key changed between calls: %q vs %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
	if !VerifyLocalKey(first, first) {
		t.Fatal("correct key was rejected")
	}
	if VerifyLocalKey(first, first+"x") {
		t.Fatal("wrong key was accepted")
	}
	if VerifyLocalKey(first, "") {
		t.Fatal("empty key was accepted")
	}
}
