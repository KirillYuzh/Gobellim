package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/id"
)

type TokenSource struct {
	AuthURL     string
	Credentials string
	Scope       string
	HTTPClient  *http.Client
	mu          sync.Mutex
	token       string
	expiresAt   time.Time
}

func (ts *TokenSource) httpClient() *http.Client {
	if ts.HTTPClient != nil {
		return ts.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.token != "" && time.Now().Before(ts.expiresAt.Add(-30*time.Second)) {
		return ts.token, nil
	}
	if ts.AuthURL == "" {
		return "", fmt.Errorf("oauth: AuthURL is required: %w", core.ErrInvalidConfig)
	}

	form := url.Values{"scope": {ts.Scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.AuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+ts.Credentials)
	req.Header.Set("RqUID", id.UUIDv4())

	resp, err := ts.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: request: %w", errors.Join(err, core.ErrProvider))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("oauth: status %d: %w", resp.StatusCode, core.ErrProvider)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("oauth: decode response: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("oauth: empty access token: %w", core.ErrProvider)
	}

	ts.token = out.AccessToken
	if out.ExpiresAt > 0 {
		ts.expiresAt = time.UnixMilli(out.ExpiresAt)
	} else {
		ts.expiresAt = time.Now().Add(25 * time.Minute)
	}
	return ts.token, nil
}
