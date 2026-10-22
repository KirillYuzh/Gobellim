// Package elasticsearch provides a concrete store.ES client over net/http. It
// is an optional plugin: the core store package owns the index schemas and
// query builders, this package owns the transport.
package elasticsearch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/store"
)

// DefaultTimeout bounds one request when Config.HTTPClient is nil.
const DefaultTimeout = 30 * time.Second

// maxResponseBytes caps one response body read.
const maxResponseBytes = 32 << 20

// Config configures the Elasticsearch client. URL is required and may carry a
// path prefix (for example https://host/es). Authentication is either APIKey or
// Username/Password, not both.
type Config struct {
	URL        string
	Username   string
	Password   string
	APIKey     string
	HTTPClient *http.Client
}

// Client sends store.Request values to Elasticsearch.
type Client struct {
	base     *url.URL
	hc       *http.Client
	username string
	password string
	apiKey   string
}

// New validates the configuration and returns a client. It returns an error
// wrapping core.ErrInvalidConfig for an empty or malformed URL or for
// conflicting credentials.
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("elasticsearch: URL is required: %w", core.ErrInvalidConfig)
	}
	base, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: parse URL: %w: %w", err, core.ErrInvalidConfig)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("elasticsearch: URL %q must be absolute (scheme and host): %w", cfg.URL, core.ErrInvalidConfig)
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = strings.TrimRight(base.RawPath, "/")
	if cfg.APIKey != "" && (cfg.Username != "" || cfg.Password != "") {
		return nil, fmt.Errorf("elasticsearch: APIKey and Username/Password are mutually exclusive: %w", core.ErrInvalidConfig)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{
		base:     base,
		hc:       hc,
		username: cfg.Username,
		password: cfg.Password,
		apiKey:   cfg.APIKey,
	}, nil
}

// Do sends one request and returns the response status and body. A non-2xx
// status is not an error: store callers classify it themselves. Transport and
// read failures are returned as errors, and so is a response body larger than
// 32 MiB. A path with a "." or ".." segment is rejected rather than resolved.
func (c *Client) Do(ctx context.Context, req store.Request) (int, []byte, error) {
	if req.Method == "" {
		return 0, nil, fmt.Errorf("elasticsearch: request method is required: %w", core.ErrInvalidConfig)
	}
	rel, err := url.Parse(req.EffectivePath())
	if err != nil {
		return 0, nil, fmt.Errorf("elasticsearch: parse path %q: %w", req.EffectivePath(), err)
	}
	if err := checkSegments(rel.Path); err != nil {
		return 0, nil, fmt.Errorf("elasticsearch: path %q: %w", req.EffectivePath(), err)
	}

	u := *c.base
	u.Path = c.base.Path + "/" + strings.TrimLeft(rel.Path, "/")
	u.RawPath = ""
	if rel.RawPath != "" {
		u.RawPath = c.base.EscapedPath() + "/" + strings.TrimLeft(rel.RawPath, "/")
	}
	u.RawQuery = rel.RawQuery

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), bytes.NewReader(req.Body))
	if err != nil {
		return 0, nil, fmt.Errorf("elasticsearch: build request: %w", err)
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	httpReq.Header.Set("Content-Type", contentType)
	switch {
	case c.apiKey != "":
		httpReq.Header.Set("Authorization", "ApiKey "+c.apiKey)
	case c.username != "" || c.password != "":
		httpReq.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return 0, nil, fmt.Errorf("elasticsearch: %s %s: %w", req.Method, req.EffectivePath(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("elasticsearch: read %s %s: %w", req.Method, req.EffectivePath(), err)
	}
	if len(body) > maxResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("elasticsearch: response exceeds %d bytes", maxResponseBytes)
	}
	return resp.StatusCode, body, nil
}

// checkSegments rejects dot segments. url.Parse unescapes them, so "%2e%2e"
// is caught here too.
func checkSegments(p string) error {
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("segment %q is not allowed", seg)
		}
	}
	return nil
}
