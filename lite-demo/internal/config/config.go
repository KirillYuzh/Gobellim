// Package config reads the environment and rejects an unusable start.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Config is everything the service reads from the environment.
type Config struct {
	Addr           string
	PolicyPath     string
	MockURL        string
	OllamaURL      string
	OllamaModel    string
	RequestTimeout time.Duration
	Env            string
}

// Load reads the environment. An error here refuses to start instead of
// silently falling back.
func Load() (Config, error) {
	c := Config{
		Addr:        env("HTTP_ADDR", ":8080"),
		PolicyPath:  env("POLICY_PATH", "policy.json"),
		MockURL:     env("MOCK_BASE_URL", "http://localhost:8081"),
		OllamaURL:   env("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel: env("OLLAMA_MODEL", "t-tech/T-lite-it-2.1:q4_K_M"),
		Env:         env("APP_ENV", "dev"),
	}
	var err error
	if c.RequestTimeout, err = time.ParseDuration(env("REQUEST_TIMEOUT", "3m")); err != nil {
		return Config{}, fmt.Errorf("REQUEST_TIMEOUT: %w", err)
	}
	switch {
	case c.Env == "prod":
		return Config{}, errors.New("lite-demo is a demo: APP_ENV=prod is not supported")
	case c.RequestTimeout <= 0:
		return Config{}, errors.New("REQUEST_TIMEOUT must be > 0")
	case c.OllamaURL == "" || c.MockURL == "":
		return Config{}, errors.New("OLLAMA_URL and MOCK_BASE_URL are required")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
