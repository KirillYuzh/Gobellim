package model

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

func httpClient(hc *http.Client) *http.Client { return httpjson.Client(hc) }

func joinURL(base, path string) string { return httpjson.JoinURL(base, path) }

func doJSON(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body, out any) error {
	if err := httpjson.Do(ctx, hc, method, url, headers, body, out); err != nil {
		return errors.Join(err, core.ErrProvider)
	}
	return nil
}

// Retryable reports whether err is a transient failure worth retrying;
// context cancellation and non-retryable statuses return false.
func Retryable(err error) bool { return httpjson.IsRetryable(err) }

// RetryAfter returns the provider-requested retry delay, or zero when err
// carries none.
func RetryAfter(err error) time.Duration { return httpjson.RetryAfter(err) }
