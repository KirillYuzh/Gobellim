package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
)

const defaultChainBackoff = 500 * time.Millisecond

// ChainConfig tunes a fallback chain: MaxRetries applies per client and Backoff
// is the base retry delay, defaulting to 500ms when not positive.
type ChainConfig struct {
	MaxRetries int
	Backoff    time.Duration
}

type chainClient struct {
	cfg          ChainConfig
	clients      []Client
	nativeSchema bool
}

// Chain returns a Client that tries clients in order, retrying retryable errors
// up to cfg.MaxRetries per client; it returns an error wrapping
// core.ErrInvalidConfig when no clients are given or any is nil.
func Chain(cfg ChainConfig, clients ...Client) (Client, error) {
	if len(clients) == 0 {
		return nil, fmt.Errorf("model: chain: at least one client is required: %w", core.ErrInvalidConfig)
	}
	for i, c := range clients {
		if c == nil {
			return nil, fmt.Errorf("model: chain: client %d is nil: %w", i, core.ErrInvalidConfig)
		}
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultChainBackoff
	}
	return &chainClient{cfg: cfg, clients: clients, nativeSchema: chainNativeSchema(clients)}, nil
}

func chainNativeSchema(clients []Client) bool {
	for _, c := range clients {
		if !SupportsNativeSchema(c) {
			return false
		}
	}
	return len(clients) > 0
}

// Model returns the first client's model name.
func (c *chainClient) Model() string { return c.clients[0].Model() }

// SupportsNativeSchema reports true only when every link supports native
// schema enforcement.
func (c *chainClient) SupportsNativeSchema() bool { return c.nativeSchema }

// Chat tries each client in order, retrying retryable errors up to MaxRetries
// per client, and returns the last error if every client fails.
func (c *chainClient) Chat(ctx context.Context, req Request) (Response, error) {
	var errs []error
	for _, client := range c.clients {
		linkReq := requestForLink(req, client)
		for attempt := 0; ; attempt++ {
			resp, err := client.Chat(ctx, linkReq)
			if err == nil {
				return resp, nil
			}
			errs = append(errs, err)
			if attempt >= c.cfg.MaxRetries || !Retryable(err) {
				break
			}
			delay := httpjson.Backoff(c.cfg.Backoff, attempt, RetryAfter(err))
			if delay <= 0 {
				return Response{}, ctx.Err()
			}
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return Response{}, ctx.Err()
			case <-t.C:
			}
		}
	}
	return Response{}, errors.Join(errs...)
}

func requestForLink(req Request, client Client) Request {
	if req.SchemaMode != SchemaNative || len(req.JSONSchema) == 0 {
		return req
	}
	if SupportsNativeSchema(client) {
		return req
	}
	req.SchemaMode = SchemaInline
	return req
}
