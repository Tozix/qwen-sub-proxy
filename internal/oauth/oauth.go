// Package oauth implements the Qwen subscription OAuth device flow against
// https://auth.qwen.ai. The protocol mirrors the one used by the official
// Qwen Code client: device code, token exchange, refresh and userinfo.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Protocol constants of the Qwen subscription OAuth service.
const (
	// BaseURL is the OAuth origin.
	BaseURL = "https://auth.qwen.ai"
	// ClientID is the public client identifier of the subscription flow.
	ClientID = "qwen-code-cli"
	// Scope requests model invocation and profile read access.
	Scope = "model:invoke profile:read"
	// DeviceCodeGrant is the grant type used for the device flow.
	DeviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"
	// RefreshGrant is the grant type used to rotate the token pair.
	RefreshGrant = "refresh_token"
)

// Error is an OAuth protocol error returned by the authorization server.
type Error struct {
	Code        string
	Description string
	Status      int
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("qwen oauth: %s: %s (status %d)", e.Code, e.Description, e.Status)
	}
	return fmt.Sprintf("qwen oauth: %s (status %d)", e.Code, e.Status)
}

// IsPending reports whether the authorization is still waiting for the user.
func IsPending(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && e.Code == "authorization_pending"
}

// IsSlowDown reports whether the client must poll less frequently.
func IsSlowDown(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && e.Code == "slow_down"
}

// IsInvalidGrant reports whether the refresh token is no longer usable.
func IsInvalidGrant(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && e.Code == "invalid_grant"
}

// DeviceAuth is the response of the device authorization request.
type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// WebOrigin is the chat origin that hosts the authorization page.
const WebOrigin = "https://chat.qwen.ai"

// AuthorizationURL builds the browser URL that confirms a device
// authorization.
//
// The server also returns verification_uri_complete, but that address
// ("/device") redirects into the regular chat. The official Qwen Code client
// builds the confirmation link itself from the user code, the way it is done
// here, so the same URL is used.
func AuthorizationURL(userCode string) string {
	query := url.Values{}
	query.Set("user_code", userCode)
	query.Set("client", "cli")
	query.Set("entry", "qwen_code")
	query.Set("paywallScene", "cli")
	return WebOrigin + "/authorize?" + query.Encode()
}

// Tokens is an OAuth token pair.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
}

// UserInfo describes the authenticated account and its membership tier.
type UserInfo struct {
	Sub        string `json:"sub"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	MemberType string `json:"member_type"`
}

// Client performs OAuth requests.
type Client struct {
	HTTP    *http.Client
	UserAge string
}

// NewClient returns a Client with sane timeouts. Streaming is not performed
// against the OAuth endpoints, so a total timeout is safe.
func NewClient(userAgent string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		UserAge: userAgent,
	}
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAge)
	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAge)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("qwen oauth: decode response: %w", err)
	}
	return nil
}

type errorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Code             string `json:"code"`
	Message          string `json:"message"`
}

func parseError(status int, body []byte) error {
	var parsed errorBody
	_ = json.Unmarshal(body, &parsed)
	code := parsed.Error
	if code == "" {
		code = parsed.Code
	}
	if code == "" {
		code = "invalid_response"
	}
	desc := parsed.ErrorDescription
	if desc == "" {
		desc = parsed.Message
	}
	return &Error{Code: code, Description: desc, Status: status}
}

// DeviceCode starts a device authorization.
func (c *Client) DeviceCode(ctx context.Context) (*DeviceAuth, error) {
	form := url.Values{
		"client_id": {ClientID},
		"scope":     {Scope},
	}
	var out DeviceAuth
	if err := c.postForm(ctx, "/api/oauth/device/code", form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Exchange trades a device code for a token pair.
func (c *Client) Exchange(ctx context.Context, deviceCode string) (*Tokens, error) {
	form := url.Values{
		"grant_type":  {DeviceCodeGrant},
		"client_id":   {ClientID},
		"device_code": {deviceCode},
	}
	var out Tokens
	if err := c.postForm(ctx, "/api/oauth/token", form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Refresh rotates the token pair using a refresh token.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	form := url.Values{
		"grant_type":    {RefreshGrant},
		"client_id":     {ClientID},
		"refresh_token": {refreshToken},
	}
	var out Tokens
	if err := c.postForm(ctx, "/api/oauth/token", form, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UserInfo returns the identity and membership tier of the account.
func (c *Client) UserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	var out UserInfo
	if err := c.get(ctx, "/api/oauth/userinfo", accessToken, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Revoke invalidates a refresh token server-side.
func (c *Client) Revoke(ctx context.Context, token string) error {
	form := url.Values{
		"client_id":       {ClientID},
		"token":           {token},
		"token_type_hint": {"refresh_token"},
	}
	return c.postForm(ctx, "/api/oauth/revoke", form, nil)
}
