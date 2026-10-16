package webfetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestValidateURLRejectsNonHTTPSSchemes(t *testing.T) {
	bad := []string{
		"file:///etc/passwd",
		"file://localhost/etc/passwd",
		"gopher://127.0.0.1:70/_hello",
		"data:text/plain;base64,SGVsbG8=",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"ws://example.com/socket",
		"//example.com/path",
		"",
	}
	for _, raw := range bad {
		_, err := validateURL(raw)
		if err == nil {
			t.Errorf("validateURL(%q) = nil error, want rejection", raw)
			continue
		}
		if !errors.Is(err, core.ErrInvalidArgs) {
			t.Errorf("validateURL(%q) error = %v, want ErrInvalidArgs", raw, err)
		}
	}

	good := []string{
		"http://example.com",
		"https://example.com/path?q=1#frag",
		"https://93.184.216.34/",
	}
	for _, raw := range good {
		if _, err := validateURL(raw); err != nil {
			t.Errorf("validateURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestBlockedHost(t *testing.T) {
	blockedHosts := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1",
		"::1",
		"fc00::1",
		"fe80::1",
		"0.0.0.0",
		"::ffff:127.0.0.1",
		"::ffff:10.0.0.1",
	}
	for _, host := range blockedHosts {
		if !blockedHost(host) {
			t.Errorf("blockedHost(%q) = false, want true", host)
		}
	}

	allowedHosts := []string{
		"93.184.216.34",
		"2606:2800::1",
	}
	for _, host := range allowedHosts {
		if blockedHost(host) {
			t.Errorf("blockedHost(%q) = true, want false", host)
		}
	}

	if blockedHost("example.com") {
		t.Error(`blockedHost("example.com") = true, want false`)
	}
}

func TestBlockedAddr(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1",
		"169.254.169.254",
		"100.64.0.1",
		"::1",
		"fc00::1",
		"::ffff:192.168.1.1",
	} {
		if !blocked(netip.MustParseAddr(s)) {
			t.Errorf("blocked(%s) = false, want true", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "2606:2800::1"} {
		if blocked(netip.MustParseAddr(s)) {
			t.Errorf("blocked(%s) = true, want false", s)
		}
	}
	if !blocked(netip.Addr{}) {
		t.Error("blocked(invalid) = false, want true (fail closed)")
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"example.com", "internal.example.org"}
	cases := []struct {
		host string
		want bool
	}{
		{"example.com", true},
		{"EXAMPLE.COM", true},
		{"cdn.example.com", true},
		{"a.b.example.com", true},
		{"example.com.", true},
		{"internal.example.org", true},
		{"api.internal.example.org", true},
		{"notexample.com", false},
		{"example.com.evil.com", false},
		{"example.org", false},
		{"evilinternal.example.org", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := hostAllowed(tc.host, allow); got != tc.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
	if hostAllowed("example.com", nil) {
		t.Error("hostAllowed with empty allowlist = true, want false")
	}
}

func TestConfigNormalizedDefaultsAndErrors(t *testing.T) {
	c, err := (Config{}).normalized()
	if err != nil {
		t.Fatalf("normalized() = %v, want nil", err)
	}
	if c.MaxBytes != defaultMaxBytes || c.Timeout != defaultTimeout || c.MaxRedirects != defaultMaxRedirects {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestNewRejectsNegativeConfig(t *testing.T) {
	cases := []Config{
		{MaxBytes: -1},
		{Timeout: -time.Second},
		{MaxRedirects: -1},
	}
	for _, cfg := range cases {
		_, err := New(nil, "web_fetch", cfg)
		if err == nil {
			t.Fatalf("New(%+v) = nil error, want ErrInvalidConfig", cfg)
		}
		if !errors.Is(err, core.ErrInvalidConfig) {
			t.Fatalf("New(%+v) error = %v, want ErrInvalidConfig", cfg, err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubFetcher(cfg Config, fn roundTripFunc) *fetcher {
	cfg, err := cfg.normalized()
	if err != nil {
		panic(err)
	}
	return &fetcher{cfg: cfg, client: &http.Client{Transport: fn}}
}

func TestFetchTruncatesBody(t *testing.T) {
	body := strings.Repeat("x", 10)
	f := stubFetcher(Config{MaxBytes: 4, Timeout: time.Second, MaxRedirects: 1},
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})

	out, err := f.fetch(context.Background(), "https://example.com/")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out.Status != 200 {
		t.Errorf("Status = %d, want 200", out.Status)
	}
	if out.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("ContentType = %q", out.ContentType)
	}
	if !out.Truncated {
		t.Error("Truncated = false, want true")
	}
	if out.Text != "xxxx" {
		t.Errorf("Text = %q, want %q", out.Text, "xxxx")
	}
}

func TestFetchNon2xxIsData(t *testing.T) {
	f := stubFetcher(Config{MaxBytes: 64, Timeout: time.Second, MaxRedirects: 1},
		func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 404,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("not found")),
			}, nil
		})

	out, err := f.fetch(context.Background(), "https://example.com/missing")
	if err != nil {
		t.Fatalf("non-2xx must not be an error, got %v", err)
	}
	if out.Status != 404 || out.Text != "not found" {
		t.Errorf("out = %+v, want status 404 and text %q", out, "not found")
	}
	if out.Truncated {
		t.Error("Truncated = true, want false")
	}
}

func TestFetchRejectsSchemeAndHost(t *testing.T) {
	var reached int
	f := stubFetcher(Config{AllowHosts: []string{"example.com"}, MaxBytes: 64, Timeout: time.Second, MaxRedirects: 1},
		func(*http.Request) (*http.Response, error) {
			reached++
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("ok")),
			}, nil
		})

	if _, err := f.fetch(context.Background(), "file:///etc/passwd"); !errors.Is(err, core.ErrInvalidArgs) {
		t.Errorf("file scheme error = %v, want ErrInvalidArgs", err)
	}
	if _, err := f.fetch(context.Background(), "https://evil.com/"); !errors.Is(err, core.ErrUnauthorized) {
		t.Errorf("disallowed host error = %v, want ErrUnauthorized", err)
	}
	if reached != 0 {
		t.Errorf("transport reached %d times for rejected requests, want 0", reached)
	}

	if _, err := f.fetch(context.Background(), "https://cdn.example.com/"); err != nil {
		t.Errorf("allowed subdomain error = %v, want nil", err)
	}
	if reached != 1 {
		t.Errorf("transport reached %d times for the allowed request, want 1", reached)
	}
}
