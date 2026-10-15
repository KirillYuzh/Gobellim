package model

import (
	"context"
	"errors"
	"net/http"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/httpjson"
)

func httpClient(hc *http.Client) *http.Client { return httpjson.Client(hc) }

func joinURL(base, path string) string { return httpjson.JoinURL(base, path) }

func doJSON(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body, out any) error {
	if err := httpjson.Do(ctx, hc, method, url, headers, body, out); err != nil {
		return errors.Join(err, core.ErrProvider)
	}
	return nil
}
