package elastic

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gobellim/kit/store"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(baseURL string, hc *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), hc: hc}
}

func (c *Client) Do(ctx context.Context, req store.Request) (int, []byte, error) {
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, c.base+req.EffectivePath(), body)
	if err != nil {
		return 0, nil, err
	}
	if len(req.Body) > 0 {
		ct := req.ContentType
		if ct == "" {
			ct = "application/json"
		}
		httpReq.Header.Set("Content-Type", ct)
	}
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}
