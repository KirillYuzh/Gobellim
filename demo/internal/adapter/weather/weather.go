package weather

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gobellim/kit/demo/internal/domain"
)

const (
	defaultBaseURL = "https://wttr.in"
	answerFormat   = "%l|%C|%t|%w"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(hc *http.Client, base string) *Client {
	if strings.TrimSpace(base) == "" {
		base = defaultBaseURL
	}
	return &Client{base: strings.TrimRight(base, "/"), hc: hc}
}

func (c *Client) Current(ctx context.Context, city string) (domain.Weather, error) {
	city = strings.TrimSpace(city)
	if city == "" {
		return domain.Weather{}, fmt.Errorf("city is required: %w", domain.ErrInvalidInput)
	}

	endpoint := fmt.Sprintf("%s/%s?format=%s&lang=ru", c.base, url.PathEscape(city), answerFormat)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.Weather{}, fmt.Errorf("weather: build request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return domain.Weather{}, fmt.Errorf("weather: %w: %w", err, domain.ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return domain.Weather{}, fmt.Errorf("weather: read response: %w: %w", err, domain.ErrUnavailable)
	}
	text := strings.TrimSpace(string(body))
	if resp.StatusCode != http.StatusOK {
		if strings.Contains(text, "not found") {
			return domain.Weather{}, fmt.Errorf("weather: city %q: %w", city, domain.ErrNotFound)
		}
		return domain.Weather{}, fmt.Errorf("weather: status %d: %w", resp.StatusCode, domain.ErrUnavailable)
	}
	return parse(city, text)
}

func parse(city, text string) (domain.Weather, error) {
	fields := strings.Split(text, "|")
	if len(fields) < 4 {
		return domain.Weather{}, fmt.Errorf("weather: unexpected response %q: %w", text, domain.ErrUnavailable)
	}
	place := strings.TrimSpace(fields[0])
	if place == "" {
		place = city
	}
	temperature, _ := firstNumber(fields[2])
	wind, _ := firstNumber(fields[3])
	return domain.Weather{
		City:         place,
		Description:  strings.TrimSpace(fields[1]),
		TemperatureC: temperature,
		WindKmh:      wind,
	}, nil
}

func firstNumber(s string) (float64, bool) {
	start := strings.IndexFunc(s, isNumeric)
	if start < 0 {
		return 0, false
	}
	end := len(s) - start
	if i := strings.IndexFunc(s[start:], func(r rune) bool { return !isNumeric(r) }); i >= 0 {
		end = i
	}
	v, err := strconv.ParseFloat(s[start:start+end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func isNumeric(r rune) bool {
	return r == '+' || r == '-' || r == '.' || (r >= '0' && r <= '9')
}
