package tokenmgr

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qwen-sub-proxy/internal/creds"
	"qwen-sub-proxy/internal/oauth"
)

// fakeRefresher counts refresh calls and returns a fixed token pair.
type fakeRefresher struct {
	calls  atomic.Int32
	delay  time.Duration
	err    error
	tokens *oauth.Tokens
}

func (f *fakeRefresher) Refresh(context.Context, string) (*oauth.Tokens, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.tokens, nil
}

func TestAccessTokenRefreshesExpiredToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	stored := &creds.Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := creds.Save(path, stored); err != nil {
		t.Fatalf("save: %v", err)
	}
	refresher := &fakeRefresher{tokens: &oauth.Tokens{AccessToken: "new", RefreshToken: "refresh2", ExpiresIn: 3600}}
	manager, err := New(path, refresher)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	token, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	if token != "new" {
		t.Fatalf("token = %q, want new", token)
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refresher.calls.Load())
	}
}

func TestConcurrentAccessTokenRefreshesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	stored := &creds.Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := creds.Save(path, stored); err != nil {
		t.Fatalf("save: %v", err)
	}
	refresher := &fakeRefresher{
		delay:  50 * time.Millisecond,
		tokens: &oauth.Tokens{AccessToken: "new", RefreshToken: "refresh2", ExpiresIn: 3600},
	}
	manager, err := New(path, refresher)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	tokens := make([]string, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokens[idx], errs[idx] = manager.AccessToken(context.Background())
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if tokens[i] != "new" {
			t.Fatalf("worker %d token = %q, want new", i, tokens[i])
		}
	}
	if calls := refresher.calls.Load(); calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (single-flight)", calls)
	}
}

func TestConcurrentForceRefreshRotatesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	stored := &creds.Credentials{
		AccessToken:  "stale",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}
	if err := creds.Save(path, stored); err != nil {
		t.Fatalf("save: %v", err)
	}
	refresher := &fakeRefresher{
		delay:  50 * time.Millisecond,
		tokens: &oauth.Tokens{AccessToken: "new", RefreshToken: "refresh2", ExpiresIn: 3600},
	}
	manager, err := New(path, refresher)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	const workers = 20
	var wg sync.WaitGroup
	tokens := make([]string, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokens[idx], errs[idx] = manager.ForceRefresh(context.Background())
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if tokens[i] != "new" {
			t.Fatalf("worker %d token = %q, want new", i, tokens[i])
		}
	}
	if calls := refresher.calls.Load(); calls != 1 {
		t.Fatalf("refresh calls = %d, want 1 (single-flight rotation)", calls)
	}
}

func TestRefreshKeepsCurrentTokenWhenResponseOmitsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	stored := &creds.Credentials{
		AccessToken:  "stale",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := creds.Save(path, stored); err != nil {
		t.Fatalf("save: %v", err)
	}
	refresher := &fakeRefresher{tokens: &oauth.Tokens{AccessToken: "new", ExpiresIn: 3600}}
	manager, err := New(path, refresher)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := manager.AccessToken(context.Background()); err != nil {
		t.Fatalf("access token: %v", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.RefreshToken != "refresh" {
		t.Fatalf("refresh token = %q, want the previous one to be kept", snapshot.RefreshToken)
	}
}

func TestInvalidGrantMarksReauthentication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	stored := &creds.Credentials{
		AccessToken:  "old",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	}
	if err := creds.Save(path, stored); err != nil {
		t.Fatalf("save: %v", err)
	}
	refresher := &fakeRefresher{err: &oauth.Error{Code: "invalid_grant", Status: 400}}
	manager, err := New(path, refresher)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := manager.AccessToken(context.Background()); !errors.Is(err, ErrReauthentication) {
		t.Fatalf("err = %v, want ErrReauthentication", err)
	}
	if !manager.NeedsReauth() {
		t.Fatal("NeedsReauth() = false, want true")
	}
	if _, err := creds.Load(path); err != nil {
		t.Fatalf("credentials must be kept after invalid_grant: %v", err)
	}
}
