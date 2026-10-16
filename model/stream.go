package model

import "context"

// Streamer is implemented by clients that can stream content deltas. ChatStream
// returns the same Response Chat would, while invoking onDelta for every text
// delta in order. A non-nil error from onDelta aborts the stream and is
// returned to the caller.
type Streamer interface {
	ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Response, error)
}
