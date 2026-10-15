package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gobellim/kit/demo/internal/domain"
)

type Auth struct {
	directory Directory
	tokens    TokenIssuer
	ttl       time.Duration
	tenant    string
}

func NewAuth(directory Directory, tokens TokenIssuer, ttl time.Duration) *Auth {
	return &Auth{directory: directory, tokens: tokens, ttl: ttl, tenant: domain.Tenant}
}

func (a *Auth) Login(ctx context.Context, login string) (domain.Session, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return domain.Session{}, fmt.Errorf("login is required: %w", domain.ErrInvalidInput)
	}
	user, err := a.directory.Find(ctx, login)
	if err != nil {
		return domain.Session{}, err
	}
	return a.tokens.Issue(ctx, user, a.ttl)
}

func (a *Auth) Authenticate(ctx context.Context, token string) (domain.Principal, error) {
	if strings.TrimSpace(token) == "" {
		return domain.Principal{}, domain.ErrUnauthorized
	}
	user, err := a.tokens.Verify(ctx, token)
	if err != nil {
		return domain.Principal{}, err
	}
	return domain.Principal{
		UserID: user.ID,
		Login:  user.Login,
		Role:   user.Role,
		Tenant: a.tenant,
	}, nil
}
