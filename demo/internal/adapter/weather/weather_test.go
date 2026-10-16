package weather

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KirillYuzh/Gobellim/demo/internal/domain"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		city     string
		text     string
		wantCity string
		wantTemp float64
		wantWind float64
		wantDesc string
	}{
		{"full", "Москва", "Москва|Слабый туман|+9°C|↓9km/h", "Москва", 9, 9, "Слабый туман"},
		{"negative temperature", "Yakutsk", "Yakutsk|Snow|-31°C|→4km/h", "Yakutsk", -31, 4, "Snow"},
		{"empty place falls back to the query", "Kazan", "|Clear|+1°C|2km/h", "Kazan", 1, 2, "Clear"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parse(tt.city, tt.text)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got.City != tt.wantCity || got.TemperatureC != tt.wantTemp || got.WindKmh != tt.wantWind || got.Description != tt.wantDesc {
				t.Fatalf("weather = %+v", got)
			}
		})
	}
}

func TestParseRejectsShortResponse(t *testing.T) {
	if _, err := parse("Moscow", "Moscow"); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestCurrentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/%D0%9C%D0%BE%D1%81%D0%BA%D0%B2%D0%B0?format=%l|%C|%t|%w&lang=ru" {
			t.Errorf("request = %q", got)
		}
		_, _ = io.WriteString(w, "Москва|Ясно|+12°C|←5km/h")
	}))
	defer srv.Close()

	got, err := New(srv.Client(), srv.URL).Current(context.Background(), "Москва")
	if err != nil {
		t.Fatal(err)
	}
	if got.City != "Москва" || got.TemperatureC != 12 || got.WindKmh != 5 || got.Description != "Ясно" {
		t.Fatalf("weather = %+v", got)
	}
}

func TestCurrentMapsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "location not found: location not found")
	}))
	defer srv.Close()

	client := New(srv.Client(), srv.URL)
	if _, err := client.Current(context.Background(), "Nowhere"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := client.Current(context.Background(), "  "); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
