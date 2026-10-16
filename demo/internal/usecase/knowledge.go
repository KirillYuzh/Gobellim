package usecase

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
)

const (
	maxDocID      = 128
	maxDocTitle   = 200
	maxDocText    = 200_000
	maxDocACL     = 16
	defaultKbTopK = 3
	maxKbTopK     = 10
)

type Knowledge struct {
	kb KnowledgeBase
}

func NewKnowledge(kb KnowledgeBase) *Knowledge {
	return &Knowledge{kb: kb}
}

func (k *Knowledge) List(ctx context.Context, p domain.Principal) ([]domain.KnowledgeDoc, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	return k.kb.List(ctx, p)
}

func (k *Knowledge) Get(ctx context.Context, p domain.Principal, id string) (domain.KnowledgeDoc, error) {
	if err := requireAdmin(p); err != nil {
		return domain.KnowledgeDoc{}, err
	}
	id = strings.TrimSpace(id)
	if err := validateID(id); err != nil {
		return domain.KnowledgeDoc{}, err
	}
	return k.kb.Get(ctx, p, id)
}

func (k *Knowledge) Create(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	doc, err := normalize(doc)
	if err != nil {
		return err
	}
	return k.kb.Create(ctx, p, doc)
}

func (k *Knowledge) Update(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	doc, err := normalize(doc)
	if err != nil {
		return err
	}
	return k.kb.Update(ctx, p, doc)
}

func (k *Knowledge) Delete(ctx context.Context, p domain.Principal, id string) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if err := validateID(id); err != nil {
		return err
	}
	return k.kb.Delete(ctx, p, id)
}

func (k *Knowledge) Search(ctx context.Context, p domain.Principal, query string, topK int) ([]domain.KnowledgeHit, error) {
	if p.UserID == "" {
		return nil, domain.ErrUnauthorized
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required: %w", domain.ErrInvalidInput)
	}
	if len([]rune(query)) > 500 {
		return nil, fmt.Errorf("query is too long: %w", domain.ErrInvalidInput)
	}
	if topK <= 0 {
		topK = defaultKbTopK
	}
	if topK > maxKbTopK {
		topK = maxKbTopK
	}
	return k.kb.Search(ctx, p, query, topK)
}

func (k *Knowledge) Seed(ctx context.Context, docs []domain.KnowledgeDoc) (int, error) {
	system := domain.Principal{UserID: "u-system", Login: "system", Role: domain.RoleAdmin, Tenant: domain.Tenant}

	existing, err := k.kb.List(ctx, system)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(existing))
	for _, d := range existing {
		known[d.ID] = true
	}

	created := 0
	for _, doc := range docs {
		if known[doc.ID] {
			continue
		}
		normalized, err := normalize(doc)
		if err != nil {
			return created, err
		}
		if err := k.kb.Create(ctx, system, normalized); err != nil {
			return created, fmt.Errorf("seed %s: %w", doc.ID, err)
		}
		created++
	}
	return created, nil
}

func requireAdmin(p domain.Principal) error {
	if p.UserID == "" {
		return domain.ErrUnauthorized
	}
	if p.Role != domain.RoleAdmin {
		return fmt.Errorf("role %q cannot manage the knowledge base: %w", p.Role, domain.ErrForbidden)
	}
	return nil
}

func normalize(doc domain.KnowledgeDoc) (domain.KnowledgeDoc, error) {
	doc.ID = strings.TrimSpace(doc.ID)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.Text = strings.TrimSpace(doc.Text)
	if err := validateID(doc.ID); err != nil {
		return domain.KnowledgeDoc{}, err
	}
	if doc.Title == "" || len(doc.Title) > maxDocTitle {
		return domain.KnowledgeDoc{}, fmt.Errorf("title is required and must be under %d bytes: %w", maxDocTitle, domain.ErrInvalidInput)
	}
	if doc.Text == "" || len(doc.Text) > maxDocText {
		return domain.KnowledgeDoc{}, fmt.Errorf("text is required and must be under %d bytes: %w", maxDocText, domain.ErrInvalidInput)
	}
	if len(doc.ACL) == 0 {
		doc.ACL = []string{string(domain.RoleUser), string(domain.RoleAdmin)}
	}
	if len(doc.ACL) > maxDocACL {
		return domain.KnowledgeDoc{}, fmt.Errorf("acl holds at most %d roles: %w", maxDocACL, domain.ErrInvalidInput)
	}
	return doc, nil
}

func validateID(id string) error {
	if id == "" || len(id) > maxDocID {
		return fmt.Errorf("id is required and must be under %d bytes: %w", maxDocID, domain.ErrInvalidInput)
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("id %q contains an unsupported character: %w", id, domain.ErrInvalidInput)
	}
	return nil
}
