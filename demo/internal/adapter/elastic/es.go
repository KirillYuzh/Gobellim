package elastic

import (
	"context"
	"io"
	"net/http"
	"strings"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(baseURL string, hc *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), hc: hc}
}

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		if strings.HasSuffix(path, "_bulk") || strings.HasSuffix(path, "_msearch") {
			req.Header.Set("Content-Type", "application/x-ndjson")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}
