package usecase

import (
	"context"
	"time"

	"github.com/gobellim/kit/demo/internal/domain"
)

type Directory interface {
	Find(ctx context.Context, login string) (domain.User, error)
}

type TokenIssuer interface {
	Issue(ctx context.Context, user domain.User, ttl time.Duration) (domain.Session, error)
	Verify(ctx context.Context, token string) (domain.User, error)
}

type WeatherProvider interface {
	Current(ctx context.Context, city string) (domain.Weather, error)
}

type ChatEngine interface {
	Ask(ctx context.Context, req ChatRequest) (domain.Answer, error)
}

type ChatRequest struct {
	Principal domain.Principal
	System    string
	Prompt    string
}
