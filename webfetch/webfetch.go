package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/internal/core"
)

const (
	defaultMaxBytes     int64 = 1 << 20
	defaultTimeout            = 15 * time.Second
	defaultMaxRedirects       = 5
	defaultToolName           = "web_fetch"
	defaultUserAgent          = "kit-webfetch/1"
)

type Config struct {
	AllowHosts   []string
	MaxBytes     int64
	Timeout      time.Duration
	MaxRedirects int
	Headers      map[string]string
}

type In struct {
	URL string `json:"url"`
}

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
	if c.MaxRedirects < 0 {
		return c, fmt.Errorf("webfetch: MaxRedirects must not be negative: %w", core.ErrInvalidConfig)
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	if c.MaxRedirects == 0 {
		c.MaxRedirects = defaultMaxRedirects
	}
	return c, nil
}

type fetcher struct {
	cfg    Config
	client *http.Client
}

func New(app *kit.App, name string, cfg Config) (*kit.ToolDef, error) {
	c, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = defaultToolName
	}
	f := &fetcher{cfg: c, client: newClient(c)}

	return kit.Tool(app, name, func(ctx context.Context, in In) (Out, error) {
		return f.fetch(ctx, in.URL)
	},
		kit.Desc("Fetch a URL over http or https and return the response body as text. Only public hosts are reachable."),
		kit.Effects(kit.EffectEgress),
		kit.EgressHosts(c.AllowHosts...),
	)
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
			if len(via) >= cfg.MaxRedirects {
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
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}
