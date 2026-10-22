package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const DefaultTimeout = 60 * time.Second

const DefaultMaxResponseBytes int64 = 32 << 20

const DefaultBackoff = 200 * time.Millisecond

type StatusError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status %d", e.StatusCode)
}

func (e *StatusError) IsRetryable() bool {
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return e.StatusCode >= 500 && e.StatusCode <= 599
}

type Config struct {
	Timeout          time.Duration
	MaxRetries       int
	Backoff          time.Duration
	MaxResponseBytes int64
}

func NewClient(cfg Config) *http.Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = DefaultBackoff
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = DefaultMaxResponseBytes
	}
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: &configuredTransport{base: http.DefaultTransport, cfg: cfg},
	}
}

func Client(hc *http.Client) *http.Client {
	if hc != nil {
		return hc
	}
	return &http.Client{Timeout: DefaultTimeout}
}

type configuredTransport struct {
	base http.RoundTripper
	cfg  Config
}

func (t *configuredTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req)
}

func configOf(hc *http.Client) Config {
	if hc == nil {
		return Config{}
	}
	if t, ok := hc.Transport.(*configuredTransport); ok {
		return t.cfg
	}
	return Config{}
}

func Do(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body, out any) error {
	var payload []byte
	hasBody := body != nil
	if hasBody {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		payload = b
	}

	cfg := configOf(hc)
	limit := cfg.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	attempts := cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var (
		lastErr    error
		retryAfter time.Duration
	)
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := wait(ctx, Backoff(cfg.Backoff, attempt, retryAfter)); err != nil {
				return err
			}
		}
		err := doOnce(ctx, hc, method, url, headers, payload, hasBody, out, limit)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryable(err) {
			return err
		}
		retryAfter = RetryAfter(err)
	}
	return lastErr
}

func doOnce(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, payload []byte, hasBody bool, out any, limit int64) error {
	var rdr io.Reader
	if hasBody {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %w", method, url, &StatusError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Body:       Snippet(data),
		})
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.IsRetryable()
	}
	var ne net.Error
	return errors.As(err, &ne)
}

func RetryAfter(err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

func JoinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" || strings.HasPrefix(path, "/") {
		return base + path
	}
	return base + "/" + path
}

func Snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

func Backoff(base time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	// A hostile or broken upstream must not be able to pin a goroutine for
	// arbitrary long: cap Retry-After at 30 seconds.
	if retryAfter > 30*time.Second {
		retryAfter = 30 * time.Second
	}
	if retryAfter > 0 {
		return retryAfter
	}
	if base <= 0 {
		return 0
	}
	d := base * time.Duration(attempt)
	if d <= 0 {
		d = base
	}
	jitter := time.Duration(rand.Int63n(int64(d/2) + 1))
	return d + jitter
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
