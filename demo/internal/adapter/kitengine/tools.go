package kitengine

import (
	"context"
	"fmt"

	"github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
)

const kbToolTopK = 3

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

type kbSearchIn struct {
	Query string `json:"query"`
}

type kbSearchOut struct {
	Results []kbSearchResult `json:"results"`
}

type kbSearchResult struct {
	DocID string `json:"doc_id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (e *Engine) kbSearch(ctx context.Context, in kbSearchIn) (kbSearchOut, error) {
	principal, ok := kit.PrincipalFrom(ctx)
	if !ok {
		return kbSearchOut{}, fmt.Errorf("no principal in tool context: %w", domain.ErrUnauthorized)
	}
	hits, err := e.Search(ctx, domainPrincipal(principal), in.Query, kbToolTopK)
	if err != nil {
		return kbSearchOut{}, err
	}
	out := kbSearchOut{Results: make([]kbSearchResult, 0, len(hits))}
	for _, h := range hits {
		out.Results = append(out.Results, kbSearchResult{DocID: h.DocID, Title: h.Title, Text: h.Text})
	}
	return out, nil
}
