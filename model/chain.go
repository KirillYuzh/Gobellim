package model

import (
	"context"
	"fmt"
	"time"

	"github.com/gobellim/kit/internal/core"
)

const defaultChainBackoff = 500 * time.Millisecond

type ChainConfig struct {
	MaxRetries int
	Backoff    time.Duration
}

type chainClient struct {
	cfg     ChainConfig
	clients []Client
}

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
	return &chainClient{cfg: cfg, clients: clients}, nil
}

func (c *chainClient) Model() string { return c.clients[0].Model() }

func (c *chainClient) Chat(ctx context.Context, req Request) (Response, error) {
	var lastErr error
	for _, client := range c.clients {
		for attempt := 0; ; attempt++ {
			resp, err := client.Chat(ctx, req)
			if err == nil {
				return resp, nil
			}
			lastErr = err
			if attempt >= c.cfg.MaxRetries || !Retryable(err) {
				break
			}
			if werr := sleepBackoff(ctx, c.cfg.Backoff, attempt, RetryAfter(err)); werr != nil {
				return Response{}, werr
			}
		}
	}
	return Response{}, lastErr
}

func sleepBackoff(ctx context.Context, base time.Duration, attempt int, retryAfter time.Duration) error {
	delay := retryAfter
	if delay <= 0 {
		delay = base * time.Duration(attempt+1)
	}
	if delay <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
