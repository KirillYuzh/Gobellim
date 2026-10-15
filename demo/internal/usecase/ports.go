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

type KnowledgeBase interface {
	List(ctx context.Context, p domain.Principal) ([]domain.KnowledgeDoc, error)
	Get(ctx context.Context, p domain.Principal, id string) (domain.KnowledgeDoc, error)
	Create(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error
	Update(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error
	Delete(ctx context.Context, p domain.Principal, id string) error
	Search(ctx context.Context, p domain.Principal, query string, topK int) ([]domain.KnowledgeHit, error)
}

type ChatRequest struct {
	Principal domain.Principal
	System    string
	Prompt    string
}
