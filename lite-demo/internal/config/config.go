// Package config читает окружение и отклоняет непригодный старт.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Config — всё, что сервис читает из окружения.
type Config struct {
	Addr            string
	PolicyPath      string
	GigaChatKey     string
	GigaChatModel   string
	GigaChatBaseURL string
	GigaChatAuthURL string
	GigaChatScope   string
	RequestTimeout  time.Duration
	Env             string
}

// Load читает окружение. Ошибка здесь отказывает в старте, а не подставляет
// молчаливое значение.
func Load() (Config, error) {
	c := Config{
		Addr:            env("HTTP_ADDR", ":8080"),
		PolicyPath:      env("POLICY_PATH", "policy.json"),
		GigaChatKey:     os.Getenv("GIGACHAT_API_KEY"),
		GigaChatModel:   env("GIGACHAT_MODEL", "GigaChat-2"),
		GigaChatBaseURL: env("GIGACHAT_BASE_URL", "https://api.giga.chat"),
		GigaChatAuthURL: env("GIGACHAT_AUTH_URL", "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"),
		GigaChatScope:   env("GIGACHAT_SCOPE", "GIGACHAT_API_PERS"),
		Env:             env("APP_ENV", "dev"),
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
	case c.GigaChatKey == "":
		return Config{}, errors.New("GIGACHAT_API_KEY is required: put it in lite-demo/.env")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
