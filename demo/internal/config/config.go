package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gobellim/kit/trace"
)

type Config struct {
	Addr            string
	ESURL           string
	WeatherBaseURL  string
	WeatherHosts    []string
	EmbedBaseURL    string
	EmbedModel      string
	EmbedDim        int
	EmbedLocal      bool
	ModelsDir       string
	GLiNERModel     string
	SeedKB          bool
	PolicyPath      string
	Credentials     string
	GigaChatBaseURL string
	GigaChatAuthURL string
	GigaChatModel   string
	CAFile          string
	HTTPTimeout     time.Duration
	TokenTTL        time.Duration

	TraceText   trace.TextPolicy
	TraceSample string
}

func Load() (Config, error) {
	if err := loadDotEnv(); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Addr:            env("KIT_ADDR", ":8080"),
		ESURL:           env("KIT_ES_URL", "http://localhost:9200"),
		WeatherBaseURL:  env("WEATHER_BASE_URL", "https://wttr.in"),
		EmbedBaseURL:    env("EMBED_BASE_URL", "http://localhost:8088/v1"),
		EmbedModel:      env("EMBED_MODEL", "intfloat/multilingual-e5-small"),
		EmbedDim:        envInt("EMBED_DIM", 384),
		EmbedLocal:      envBool("EMBED_LOCAL", false),
		ModelsDir:       env("KIT_MODELS_DIR", "models"),
		GLiNERModel:     env("GLINER_MODEL", "hivetrace__gliner-guard-omni"),
		SeedKB:          envBool("SEED_KB", true),
		PolicyPath:      env("KIT_POLICY", "demo/policy.yaml"),
		Credentials:     env("GIGACHAT_CREDENTIALS", ""),
		GigaChatBaseURL: env("GIGACHAT_BASE_URL", ""),
		GigaChatAuthURL: env("GIGACHAT_AUTH_URL", ""),
		GigaChatModel:   env("GIGACHAT_MODEL", "GigaChat-2"),
		CAFile:          env("GIGACHAT_CA_FILE", ""),
		HTTPTimeout:     envDuration("KIT_HTTP_TIMEOUT", 60*time.Second),
		TokenTTL:        envDuration("KIT_TOKEN_TTL", 12*time.Hour),
	}
	if cfg.Credentials == "" {
		return Config{}, fmt.Errorf("GIGACHAT_CREDENTIALS is required: put the base64 clientID:clientSecret pair in .env")
	}

	if u, err := url.Parse(cfg.WeatherBaseURL); err == nil && u.Hostname() != "" {
		cfg.WeatherHosts = []string{u.Hostname()}
	}

	cfg.TraceSample = env("TRACE_SAMPLE", "always")
	switch strings.ToLower(env("TRACE_TEXT", "masked")) {
	case "", "masked":
		cfg.TraceText = trace.TextMasked
	case "full":
		cfg.TraceText = trace.TextFull
	default:
		return Config{}, fmt.Errorf("TRACE_TEXT must be masked or full")
	}
	return cfg, nil
}

func loadDotEnv() error {
	for _, path := range []string{".env", "demo/.env"} {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if os.Getenv(key) == "" {
				if err := os.Setenv(key, value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return d
}

func envInt(key string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
