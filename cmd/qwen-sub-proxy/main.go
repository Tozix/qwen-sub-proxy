// Command qwen-sub-proxy exposes the Qwen subscription (chat.qwen.ai Plus/Pro)
// as a local OpenAI-compatible endpoint so that other tools — ZCode in
// particular — can use it as a regular API-key provider.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"qwen-sub-proxy/internal/config"
	"qwen-sub-proxy/internal/creds"
	"qwen-sub-proxy/internal/gateway"
	"qwen-sub-proxy/internal/oauth"
	"qwen-sub-proxy/internal/proxy"
	"qwen-sub-proxy/internal/tokenmgr"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	args := os.Args[2:]

	var err error
	switch command {
	case "serve":
		err = runServe(args)
	case "login":
		err = runLogin(args)
	case "status":
		err = runStatus(args)
	case "logout":
		err = runLogout(args)
	case "models":
		err = runModels(args)
	case "healthcheck":
		err = runHealthcheck(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `qwen-sub-proxy %s - use a chat.qwen.ai subscription from other AI tools

Usage:
  qwen-sub-proxy serve        [--host H] [--port P]   run the local proxy
  qwen-sub-proxy login                                 authorize with your Qwen account
  qwen-sub-proxy status                                show the subscription state
  qwen-sub-proxy models                                list the models of the gateway
  qwen-sub-proxy logout                                revoke and delete the credentials
  qwen-sub-proxy healthcheck [--port P]               probe a running proxy

Environment:
  %s  configuration directory (default ~/.config/qwen-sub-proxy)
  %s    fixed local API key
`, config.Version, config.EnvHome, config.EnvClientKey)
}

// runServe starts the proxy.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", config.DefaultHost, "bind address (loopback only)")
	port := fs.Int("port", config.DefaultPort, "listen port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	credsPath, err := config.CredsPath()
	if err != nil {
		return err
	}
	client := oauth.NewClient(config.UserAgent())
	manager, err := tokenmgr.New(credsPath, client)
	if err != nil {
		if errors.Is(err, creds.ErrNotFound) {
			return errors.New("no credentials found, run `qwen-sub-proxy login` first")
		}
		return err
	}

	keyPath, err := config.ClientKeyPath()
	if err != nil {
		return err
	}
	clientKey := os.Getenv(config.EnvClientKey)
	if clientKey == "" {
		clientKey, err = creds.EnsureLocalKey(keyPath)
		if err != nil {
			return err
		}
	}

	server := proxy.New(proxy.Options{
		ClientKey: clientKey,
		Tokens:    manager,
		Gateway:   gateway.NewClient(config.UserAgent()),
		Version:   config.Version,
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *host, *port),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("qwen-sub-proxy %s listening on http://%s:%d\n", config.Version, *host, *port)
		// The key itself is deliberately not printed here: the service log
		// would expose it. `qwen-sub-proxy status` prints it on demand.
		fmt.Printf("local api key file: %s\n", keyPath)
		fmt.Printf("use it in your client as base URL http://%s:%d/v1\n", *host, *port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// runLogin performs the OAuth device flow.
func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	noBrowser := fs.Bool("no-browser", false, "do not open the browser automatically")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := oauth.NewClient(config.UserAgent())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	device, err := client.DeviceCode(ctx)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	authorizationURL := oauth.AuthorizationURL(device.UserCode)
	fmt.Printf("open this URL to authorize: %s\n", authorizationURL)
	fmt.Printf("code: %s\n", device.UserCode)
	if !*noBrowser {
		if err := openBrowser(authorizationURL); err != nil {
			fmt.Fprintf(os.Stderr, "could not open the browser: %v\n", err)
		}
	}

	tokens, err := pollForToken(ctx, client, device)
	if err != nil {
		return err
	}

	credsPath, err := config.CredsPath()
	if err != nil {
		return err
	}
	stored := &creds.Credentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenType:    tokens.TokenType,
		Scope:        tokens.Scope,
		ExpiresAt:    time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix(),
		Revision:     1,
	}
	if info, err := client.UserInfo(ctx, tokens.AccessToken); err == nil {
		stored.MemberType = info.MemberType
		stored.Email = info.Email
		stored.Name = info.Name
	}
	if err := creds.Save(credsPath, stored); err != nil {
		return err
	}

	fmt.Printf("authorized: plan=%s email=%s\n", stored.MemberType, stored.Email)
	fmt.Printf("credentials stored in %s\n", credsPath)
	return nil
}

// pollForToken waits for the user to approve the device authorization.
func pollForToken(ctx context.Context, client *oauth.Client, device *oauth.DeviceAuth) (*oauth.Tokens, error) {
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if time.Now().After(deadline) {
			return nil, errors.New("authorization timed out, run the login command again")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		tokens, err := client.Exchange(ctx, device.DeviceCode)
		if err == nil {
			return tokens, nil
		}
		switch {
		case oauth.IsPending(err):
			continue
		case oauth.IsSlowDown(err):
			interval += 5 * time.Second
			continue
		default:
			return nil, fmt.Errorf("authorization failed: %w", err)
		}
	}
}

// runStatus prints the state of the subscription and the local key.
func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	credsPath, err := config.CredsPath()
	if err != nil {
		return err
	}
	keyPath, err := config.ClientKeyPath()
	if err != nil {
		return err
	}

	stored, err := creds.Load(credsPath)
	if errors.Is(err, creds.ErrNotFound) {
		fmt.Println("logged_in: false")
		return nil
	}
	if err != nil {
		return err
	}

	fmt.Printf("logged_in: true\n")
	fmt.Printf("member_type: %s\n", stored.MemberType)
	if stored.Email != "" {
		fmt.Printf("email: %s\n", stored.Email)
	}
	fmt.Printf("token_expires_in: %s\n", creds.ExpiresIn(stored).Round(time.Second))
	if key, err := creds.EnsureLocalKey(keyPath); err == nil {
		fmt.Printf("local_api_key: %s\n", key)
	}
	fmt.Printf("base_url: http://%s:%d/v1\n", config.DefaultHost, config.DefaultPort)
	return nil
}

// runModels prints the gateway model catalog.
func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	credsPath, err := config.CredsPath()
	if err != nil {
		return err
	}
	client := oauth.NewClient(config.UserAgent())
	manager, err := tokenmgr.New(credsPath, client)
	if err != nil {
		if errors.Is(err, creds.ErrNotFound) {
			return errors.New("no credentials found, run `qwen-sub-proxy login` first")
		}
		return err
	}

	ctx := context.Background()
	token, err := manager.AccessToken(ctx)
	if err != nil {
		return err
	}
	catalog, err := gateway.NewClient(config.UserAgent()).Models(ctx, token)
	if err != nil {
		return err
	}
	for _, model := range catalog.Data {
		fmt.Printf("%s\tcontext=%d\tinput=%s\n", model.ID, model.ContextWindowTokens, strings.Join(model.InputModalities, ","))
	}
	return nil
}

// runLogout revokes the refresh token and deletes the stored credentials.
func runLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	credsPath, err := config.CredsPath()
	if err != nil {
		return err
	}
	stored, err := creds.Load(credsPath)
	if errors.Is(err, creds.ErrNotFound) {
		fmt.Println("already logged out")
		return nil
	}
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := oauth.NewClient(config.UserAgent())
	if err := client.Revoke(ctx, stored.RefreshToken); err != nil {
		fmt.Fprintf(os.Stderr, "warning: server side revoke failed: %v\n", err)
	}
	if err := creds.Delete(credsPath); err != nil {
		return err
	}
	fmt.Println("logged out, local credentials removed")
	return nil
}

// runHealthcheck probes a running proxy instance.
func runHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	port := fs.Int("port", config.DefaultPort, "proxy port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	keyPath, err := config.ClientKeyPath()
	if err != nil {
		return err
	}
	key, err := creds.EnsureLocalKey(keyPath)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s:%d/health", config.DefaultHost, *port)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck failed: status %d: %s", resp.StatusCode, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	fmt.Printf("proxy ok: %v\n", payload)
	return nil
}

// openBrowser opens the authorization URL in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
