package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/gobellim/kit/demo/internal/domain"
)

type Directory struct {
	users map[string]domain.User
}

func NewDirectory(users []domain.User) *Directory {
	byLogin := make(map[string]domain.User, len(users))
	for _, u := range users {
		byLogin[u.Login] = u
	}
	return &Directory{users: byLogin}
}

func (d *Directory) Find(_ context.Context, login string) (domain.User, error) {
	user, ok := d.users[login]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return user, nil
}

type issued struct {
	user      domain.User
	expiresAt time.Time
}

type Tokens struct {
	mu    sync.Mutex
	grace time.Duration
	byKey map[string]issued
}

func NewTokens() *Tokens {
	return &Tokens{byKey: make(map[string]issued)}
}

func (t *Tokens) Issue(_ context.Context, user domain.User, ttl time.Duration) (domain.Session, error) {
	token, err := randomToken()
	if err != nil {
		return domain.Session{}, err
	}
	expiresAt := time.Now().Add(ttl)

	t.mu.Lock()
	t.byKey[token] = issued{user: user, expiresAt: expiresAt}
	t.mu.Unlock()

	return domain.Session{
		Token:     token,
		Principal: domain.Principal{UserID: user.ID, Login: user.Login, Role: user.Role},
		ExpiresAt: expiresAt,
	}, nil
}

func (t *Tokens) Verify(_ context.Context, token string) (domain.User, error) {
	t.mu.Lock()
	entry, ok := t.byKey[token]
	if ok && time.Now().After(entry.expiresAt) {
		delete(t.byKey, token)
		ok = false
	}
	t.mu.Unlock()

	if !ok {
		return domain.User{}, domain.ErrUnauthorized
	}
	return entry.user, nil
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
