package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

const (
	defaultMaxBytes     int64 = 1 << 20
	defaultTimeout            = 15 * time.Second
	defaultMaxRedirects       = 5
	defaultToolName           = "web_fetch"
	defaultUserAgent          = "gobellim-webfetch/1"
)

// Config configures the web_fetch tool. AllowHosts, when non-empty, restricts
// requests and redirects by domain boundary; MaxBytes, Timeout, and MaxRedirects
// default to 1 MiB, 15s, and 5 when zero.
type Config struct {
	AllowHosts   []string
	AllowAnyHost bool
	MaxBytes     int64
	Timeout      time.Duration
	MaxRedirects int
	Headers      map[string]string
}

// In is the tool input: a single URL to fetch.
type In struct {
	URL string `json:"url"`
}

// Out is the tool output. Status and ContentType come from the response, Text is
// the body truncated to Config.MaxBytes, and Truncated reports that truncation.
type Out struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

func (c Config) normalized() (Config, error) {
	if c.MaxBytes < 0 {
		return c, fmt.Errorf("webfetch: MaxBytes must not be negative: %w", core.ErrInvalidConfig)
	}
	if c.Timeout < 0 {
		return c, fmt.Errorf("webfetch: Timeout must not be negative: %w", core.ErrInvalidConfig)
	}
	if c.MaxRedirects < -1 {
		return c, fmt.Errorf("webfetch: MaxRedirects must not be less than -1: %w", core.ErrInvalidConfig)
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	switch c.MaxRedirects {
	case -1:
		c.MaxRedirects = 0
	case 0:
		c.MaxRedirects = defaultMaxRedirects
	}
	return c, nil
}

type fetcher struct {
	cfg    Config
	client *http.Client
}

// New declares an SSRF-hardened web_fetch tool. The tool is marked egress and
// uses AllowHosts as its host allowlist, or any host when the list is empty; it
// accepts only http and https and refuses non-public addresses.
func New(name string, cfg Config) (gobellim.ToolDef, error) {
	c, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = defaultToolName
	}
	f := &fetcher{cfg: c, client: newClient(c)}

	policy := gobellim.Egress()
	if len(c.AllowHosts) > 0 {
		policy = gobellim.Egress(c.AllowHosts...)
	} else if c.AllowAnyHost {
		policy = policy.AnyHost()
	} else {
		return nil, fmt.Errorf("webfetch: AllowHosts is empty: set AllowHosts or AllowAnyHost: %w", core.ErrInvalidConfig)
	}

	return gobellim.Tool(name, func(ctx context.Context, in In) (Out, error) {
		return f.fetch(ctx, in.URL)
	},
		gobellim.Desc("Fetch a URL over http or https and return the response body as text. Only public hosts are reachable."),
		policy,
	), nil
}

func newClient(cfg Config) *http.Client {
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            safeDialContext,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		ResponseHeaderTimeout:  cfg.Timeout,
		MaxResponseHeaderBytes: 1 << 20,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if cfg.MaxRedirects >= 0 && len(via) > cfg.MaxRedirects {
				return fmt.Errorf("webfetch: stopped after %d redirects", cfg.MaxRedirects)
			}
			switch req.URL.Scheme {
			case "http", "https":
			default:
				return fmt.Errorf("webfetch: redirect to unsupported scheme %q", req.URL.Scheme)
			}
			if len(cfg.AllowHosts) > 0 && !hostAllowed(req.URL.Hostname(), cfg.AllowHosts) {
				return fmt.Errorf("webfetch: redirect to host %q is not allowed: %w", req.URL.Hostname(), core.ErrUnauthorized)
			}
			return nil
		},
	}
}

func (f *fetcher) fetch(ctx context.Context, rawURL string) (Out, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return Out{}, err
	}
	if len(f.cfg.AllowHosts) > 0 && !hostAllowed(u.Hostname(), f.cfg.AllowHosts) {
		return Out{}, fmt.Errorf("webfetch: host %q is not allowed: %w", u.Hostname(), core.ErrUnauthorized)
	}

	ctx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Out{}, fmt.Errorf("webfetch: build request: %w: %w", err, core.ErrInvalidArgs)
	}
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return Out{}, fmt.Errorf("webfetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
	if err != nil {
		return Out{}, fmt.Errorf("webfetch: read body: %w", err)
	}
	truncated := int64(len(body)) > f.cfg.MaxBytes
	if truncated {
		body = body[:f.cfg.MaxBytes]
	}

	return Out{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Text:        string(body),
		Truncated:   truncated,
	}, nil
}

func validateURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("webfetch: url is required: %w", core.ErrInvalidArgs)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("webfetch: parse url: %w: %w", err, core.ErrInvalidArgs)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("webfetch: scheme %q is not allowed: %w", u.Scheme, core.ErrInvalidArgs)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("webfetch: url has no host: %w", core.ErrInvalidArgs)
	}
	return u, nil
}

func hostAllowed(host string, allow []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(a, ".")))
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "*.") {
			suffix := a[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && host != suffix[1:] {
				return true
			}
			continue
		}
		if host == a {
			return true
		}
	}
	return false
}
