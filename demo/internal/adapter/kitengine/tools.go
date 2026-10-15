package kitengine

import (
	"context"
	"fmt"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/demo/internal/domain"
)

type weatherIn struct {
	City string `json:"city"`
}

type weatherOut struct {
	City         string  `json:"city"`
	TemperatureC float64 `json:"temperature_c"`
	WindKmh      float64 `json:"wind_kmh"`
	Description  string  `json:"description"`
}

func (e *Engine) getWeather(ctx context.Context, in weatherIn) (weatherOut, error) {
	weather, err := e.weather.Current(ctx, in.City)
	if err != nil {
		return weatherOut{}, err
	}
	return weatherOut{
		City:         weather.City,
		TemperatureC: weather.TemperatureC,
		WindKmh:      weather.WindKmh,
		Description:  weather.Description,
	}, nil
}

type userRoleIn struct{}

type userRoleOut struct {
	Role string `json:"role"`
}

func (e *Engine) getUserRole(ctx context.Context, _ userRoleIn) (userRoleOut, error) {
	principal, ok := kit.PrincipalFrom(ctx)
	if !ok {
		return userRoleOut{}, fmt.Errorf("no principal in tool context: %w", domain.ErrUnauthorized)
	}
	if len(principal.Roles) == 0 {
		return userRoleOut{}, fmt.Errorf("principal has no role: %w", domain.ErrNotFound)
	}
	return userRoleOut{Role: principal.Roles[0]}, nil
}
