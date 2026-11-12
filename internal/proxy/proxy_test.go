package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qwen-sub-proxy/internal/creds"
	"qwen-sub-proxy/internal/gateway"
)

const testKey = "local-test-key"

// fakeTokens is a deterministic TokenProvider for the tests.
type fakeTokens struct {
	mu           sync.Mutex
	token        string
	refreshCalls atomic.Int32
	refreshErr   error
	snapshot     *creds.Credentials
}

func (f *fakeTokens) AccessToken(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token, nil
}

func (f *fakeTokens) ForceRefresh(context.Context) (string, error) {
	f.refreshCalls.Add(1)
	if f.refreshErr != nil {
		return "", f.refreshErr
	}
	f.mu.Lock()
	f.token = "refreshed-token"
	f.mu.Unlock()
	return f.token, nil
}

func (f *fakeTokens) Snapshot() *creds.Credentials { return f.snapshot }

func (f *fakeTokens) NeedsReauth() bool { return false }

// newTestProxy wires the proxy against a fake upstream gateway.
func newTestProxy(t *testing.T, upstream http.HandlerFunc, tokens TokenProvider) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		upstream(w, r)
	}))
	t.Cleanup(gatewayServer.Close)

	client := gateway.NewClient("qwen-sub-proxy/test")
	client.BaseURL = gatewayServer.URL

	server := New(Options{
		ClientKey: testKey,
		Tokens:    tokens,
		Gateway:   client,
		Version:   "test",
	})
	proxyServer := httptest.NewServer(server.Handler())
	t.Cleanup(proxyServer.Close)
	return proxyServer, &calls
}

func doRequest(t *testing.T, method, url, key string, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestRejectsMissingOrWrongLocalKey(t *testing.T) {
	tokens := &fakeTokens{token: "upstream-token"}
	proxyServer, calls := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called")
	}, tokens)

	for _, key := range []string{"", "wrong-key"} {
		resp := doRequest(t, http.MethodPost, proxyServer.URL+"/v1/chat/completions", key, `{"model":"qwen3.8-flash"}`)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body %s)", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "invalid_local_key") {
			t.Fatalf("body = %s, want invalid_local_key error", body)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0", calls.Load())
	}
}

func TestPassesThroughUpstreamResponse(t *testing.T) {
	tokens := &fakeTokens{token: "upstream-token"}
	upstreamBody := `{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`
	proxyServer, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-token" {
			t.Errorf("upstream authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, upstreamBody)
	}, tokens)

	resp := doRequest(t, http.MethodPost, proxyServer.URL+"/v1/chat/completions", testKey, `{"model":"qwen3.8-flash","messages":[]}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != upstreamBody {
		t.Fatalf("body = %s, want %s", body, upstreamBody)
	}
}

func TestPassesThroughQuotaError(t *testing.T) {
	tokens := &fakeTokens{token: "upstream-token"}
	quota := `{"error":{"type":"quota_error","code":"insufficient_quota","message":"weekly credit exhausted","quota":{"reset_at":1800000000,"tier":"plus"}}}`
	proxyServer, calls := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		io.WriteString(w, quota)
	}, tokens)

	resp := doRequest(t, http.MethodPost, proxyServer.URL+"/v1/chat/completions", testKey, `{"model":"qwen3.8-flash"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402", resp.StatusCode)
	}
	if string(body) != quota {
		t.Fatalf("body = %s, want %s", body, quota)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (no retry on 402)", calls.Load())
	}
}

func TestRefreshesAndRetriesOnceOnUnauthorized(t *testing.T) {
	tokens := &fakeTokens{token: "stale-token"}
	proxyServer, calls := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer stale-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"code":"PROXY_TOKEN_MISSING"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}, tokens)

	resp := doRequest(t, http.MethodPost, proxyServer.URL+"/v1/chat/completions", testKey, `{"model":"qwen3.8-flash"}`)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want 2 (one retry)", calls.Load())
	}
	if tokens.refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", tokens.refreshCalls.Load())
	}
}

func TestStreamsServerSentEvents(t *testing.T) {
	tokens := &fakeTokens{token: "upstream-token"}
	proxyServer, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(400 * time.Millisecond)
		io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	}, tokens)

	resp := doRequest(t, http.MethodPost, proxyServer.URL+"/v1/chat/completions", testKey, `{"model":"qwen3.8-flash","stream":true}`)
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)
	start := time.Now()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first line: %v", err)
	}
	ttfb := time.Since(start)
	if !strings.Contains(line, "first") {
		t.Fatalf("first line = %q, want the first chunk", line)
	}
	if ttfb > 350*time.Millisecond {
		t.Fatalf("time to first byte = %s, response looks buffered", ttfb)
	}
	rest, _ := io.ReadAll(reader)
	if !strings.Contains(string(rest), "second") {
		t.Fatalf("stream did not contain the second chunk: %q", rest)
	}
}

func TestModelsAreCached(t *testing.T) {
	tokens := &fakeTokens{token: "upstream-token"}
	proxyServer, calls := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"qwen3.8-max","context_window_tokens":1000000}]}`)
	}, tokens)

	for i := 0; i < 2; i++ {
		resp := doRequest(t, http.MethodGet, proxyServer.URL+"/v1/models", testKey, "")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var catalog gateway.Catalog
		if err := json.Unmarshal(body, &catalog); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(catalog.Data) != 1 || catalog.Data[0].ID != "qwen3.8-max" {
			t.Fatalf("unexpected catalog: %s", body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (catalog is cached)", calls.Load())
	}
}

func TestHealthDoesNotLeakCredentials(t *testing.T) {
	tokens := &fakeTokens{
		token:    "upstream-token",
		snapshot: &creds.Credentials{AccessToken: "secret-access", RefreshToken: "secret-refresh", MemberType: "pro"},
	}
	proxyServer, _ := newTestProxy(t, func(w http.ResponseWriter, r *http.Request) {}, tokens)

	resp := doRequest(t, http.MethodGet, proxyServer.URL+"/health", testKey, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, secret := range []string{"secret-access", "secret-refresh", "upstream-token"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("health body leaks %q: %s", secret, body)
		}
	}
	if !strings.Contains(string(body), `"member_type":"pro"`) {
		t.Fatalf("health body misses the plan: %s", body)
	}
}
