package tool

import (
	"context"
	"encoding/json"

	"github.com/KirillYuzh/Gobellim/internal/tool"
)

type viewOption[Out, ViewOut any] struct {
	f func(context.Context, Out) (ViewOut, error)
}

func (v viewOption[Out, ViewOut]) apply(o *options) {
	o.view = viewImpl[Out, ViewOut](v)
}

type viewImpl[Out, ViewOut any] struct {
	f func(context.Context, Out) (ViewOut, error)
}

func (v viewImpl[Out, ViewOut]) Apply(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var out Out
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	viewOut, err := v.f(ctx, out)
	if err != nil {
		return nil, err
	}
	return json.Marshal(viewOut)
}

var _ tool.ViewApplier = viewImpl[int, int]{}
