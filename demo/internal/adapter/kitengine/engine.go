package kitengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/demo/internal/usecase"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/store"
)

type Config struct {
	ES      store.ES
	LLM     model.Client
	Policy  *authz.Policy
	Weather usecase.WeatherProvider
	Logger  *slog.Logger
}

type Engine struct {
	app     *kit.App
	weather usecase.WeatherProvider
}

func New(ctx context.Context, cfg Config) (*Engine, error) {
	app, err := kit.New(ctx, kit.Config{
		ES:     cfg.ES,
		LLM:    cfg.LLM,
		Policy: cfg.Policy,
		Logger: cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	e := &Engine{app: app, weather: cfg.Weather}
	if _, err := kit.Tool(app, "get_weather", e.getWeather, kit.Desc("Return the current weather for a city.")); err != nil {
		return nil, err
	}
	if _, err := kit.Tool(app, "get_user_role", e.getUserRole, kit.Desc("Return the caller's role in the system.")); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Engine) Setup(ctx context.Context) error { return e.app.Setup(ctx) }

func (e *Engine) Close(ctx context.Context) error { return e.app.Close(ctx) }

func (e *Engine) Ask(ctx context.Context, req usecase.ChatRequest) (domain.Answer, error) {
	ctx = kit.WithPrincipal(ctx, kit.Principal{
		UserID: req.Principal.UserID,
		Roles:  []string{string(req.Principal.Role)},
		Tenant: req.Principal.Tenant,
	})

	answer, err := e.app.Chat(ctx, kit.ChatRequest{
		System:   req.System,
		Messages: []model.Message{{Role: model.RoleUser, Content: req.Prompt}},
	})
	if err != nil {
		return domain.Answer{}, translate(err)
	}
	return domain.Answer{Content: answer.Content}, nil
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
