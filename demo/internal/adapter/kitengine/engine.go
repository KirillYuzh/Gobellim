package kitengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	kit "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/chunk"
	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
	"github.com/KirillYuzh/Gobellim/demo/internal/usecase"
	"github.com/KirillYuzh/Gobellim/embed"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/store"
	"github.com/KirillYuzh/Gobellim/trace"
)

type Config struct {
	ES           store.ES
	LLM          model.Client
	Embedder     embed.Embedder
	Policy       *authz.Policy
	GuardL2      guard.Detector
	Weather      usecase.WeatherProvider
	WeatherHosts []string
	TraceText    trace.TextPolicy
	Sampler      trace.Sampler
	Logger       *slog.Logger
}

type Engine struct {
	app     *kit.App
	es      store.ES
	weather usecase.WeatherProvider
	guardL2 bool
}

func New(ctx context.Context, cfg Config) (*Engine, error) {
	app, err := kit.New(ctx, kit.Config{
		ES:        cfg.ES,
		LLM:       cfg.LLM,
		Embedder:  cfg.Embedder,
		Policy:    cfg.Policy,
		GuardL2:   cfg.GuardL2,
		Tokenizer: chunk.WhitespaceTokenizer{},
		TraceText: cfg.TraceText,
		Sampler:   cfg.Sampler,
		Logger:    cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	e := &Engine{app: app, es: cfg.ES, weather: cfg.Weather, guardL2: !guard.IsL1Only(cfg.GuardL2)}
	if _, err := kit.Tool(app, "kb_search", e.kbSearch,
		kit.Desc("Поиск по базе знаний поддержки: возврат и обмен, доставка, гарантия, оплата и рассрочка, поддержка, программа лояльности. Для погоды этот инструмент не подходит.")); err != nil {
		return nil, err
	}
	if _, err := kit.Tool(app, "get_weather", e.getWeather,
		kit.Desc("Текущая погода в городе."),
		kit.Effects(kit.EffectEgress),
		kit.EgressHosts(cfg.WeatherHosts...)); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Engine) Setup(ctx context.Context) error { return e.app.Setup(ctx) }

func (e *Engine) GuardL2() bool { return e.guardL2 }

func (e *Engine) Close(ctx context.Context) error { return e.app.Close(ctx) }

func (e *Engine) Ask(ctx context.Context, req usecase.ChatRequest) (domain.Answer, error) {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(req.Principal))

	answer, err := e.app.Chat(ctx, kit.ChatRequest{
		System:   req.System,
		Messages: []model.Message{{Role: model.RoleUser, Content: req.Prompt}},
	})
	if err != nil {
		return domain.Answer{}, translate(err)
	}
	return domain.Answer{Content: answer.Message.Content}, nil
}

func kitPrincipal(p domain.Principal) kit.Principal {
	return kit.Principal{UserID: p.UserID, Roles: []string{string(p.Role)}, Tenant: p.Tenant}
}

func domainPrincipal(p kit.Principal) domain.Principal {
	role := domain.RoleUser
	if len(p.Roles) > 0 {
		role = domain.Role(p.Roles[0])
	}
	return domain.Principal{UserID: p.UserID, Role: role, Tenant: p.Tenant}
}

func translate(err error) error {
	switch {
	case errors.Is(err, kit.ErrGuardReject):
		return fmt.Errorf("rejected by guard: %w: %w", err, domain.ErrInvalidInput)
	case errors.Is(err, kit.ErrGuardUnavailable):
		return fmt.Errorf("guard unavailable: %w: %w", err, domain.ErrUnavailable)
	case errors.Is(err, kit.ErrUnauthorized):
		return err
	case errors.Is(err, kit.ErrInvalidArgs):
		return fmt.Errorf("invalid arguments: %w: %w", err, domain.ErrInvalidInput)
	case errors.Is(err, kit.ErrToolNotFound):
		return fmt.Errorf("unknown tool: %w: %w", err, domain.ErrInvalidInput)
	case errors.Is(err, kit.ErrMaxSteps):
		return fmt.Errorf("tool loop limit: %w: %w", err, domain.ErrUnavailable)
	case errors.Is(err, kit.ErrProvider):
		return fmt.Errorf("model provider: %w: %w", err, domain.ErrUnavailable)
	default:
		return err
	}
}
