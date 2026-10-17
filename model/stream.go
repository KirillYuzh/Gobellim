package model

import "context"

// Streamer is an optional Client extension for streaming output; clients
// without it are used through buffered Chat.
type Streamer interface {
	ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Response, error)
}
