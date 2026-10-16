package model

import "context"

type Streamer interface {
	ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Response, error)
}
