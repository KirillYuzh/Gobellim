package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/demo/internal/adapter/elastic"
	"github.com/gobellim/kit/demo/internal/adapter/guardrail"
	"github.com/gobellim/kit/demo/internal/adapter/httpapi"
	"github.com/gobellim/kit/demo/internal/adapter/kitengine"
	"github.com/gobellim/kit/demo/internal/adapter/memory"
	"github.com/gobellim/kit/demo/internal/adapter/weather"
	"github.com/gobellim/kit/demo/internal/config"
	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/demo/internal/seed"
	"github.com/gobellim/kit/demo/internal/usecase"
	"github.com/gobellim/kit/embed"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/trace"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bot stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	client, err := newHTTPClient(cfg)
	if err != nil {
		return err
	}

	policy, err := authz.Load(cfg.PolicyPath)
	if err != nil {
		return err
	}
	llm, err := model.GigaChat(model.GigaChatConfig{
		BaseURL:     cfg.GigaChatBaseURL,
		AuthURL:     cfg.GigaChatAuthURL,
		Credentials: cfg.Credentials,
		Model:       cfg.GigaChatModel,
		HTTPClient:  client,
	})
	if err != nil {
		return err
	}
	embedder, err := newEmbedder(cfg, client)
	if err != nil {
		return err
	}

	sampler, err := trace.SamplerFromString(cfg.TraceSample)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	engine, err := kitengine.New(ctx, kitengine.Config{
		ES:           elastic.New(cfg.ESURL, client),
		LLM:          llm,
		Embedder:     embedder,
		Policy:       policy,
		GuardL2:      guardrail.New(cfg.ModelsDir, cfg.GLiNERModel, log),
		Weather:      weather.New(client, cfg.WeatherBaseURL),
		WeatherHosts: cfg.WeatherHosts,
		TraceText:    cfg.TraceText,
		Sampler:      sampler,
		Logger:       log,
	})
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close(context.Background()) }()

	if err := engine.Setup(ctx); err != nil {
		return err
	}

	directory := memory.NewDirectory([]domain.User{
		{ID: "u-alice", Login: "alice", Role: domain.RoleUser},
		{ID: "u-root", Login: "root", Role: domain.RoleAdmin},
	})
	auth := usecase.NewAuth(directory, memory.NewTokens(), cfg.TokenTTL)
	bot := usecase.NewBot(engine)
	kb := usecase.NewKnowledge(engine)

	if cfg.SeedKB {
		docs, err := seed.Docs()
		if err != nil {
			return err
		}
		created, err := kb.Seed(ctx, docs)
		if err != nil {
			log.Warn("knowledge base not seeded", "err", err)
		} else {
			log.Info("knowledge base ready", "seeded", created, "documents", len(docs))
		}
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(auth, bot, kb, log),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Info("bot listening",
		"addr", cfg.Addr, "elasticsearch", cfg.ESURL, "embeddings", cfg.EmbedBaseURL,
		"policy", cfg.PolicyPath, "guard_l2", engine.GuardL2())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newEmbedder(cfg config.Config, client *http.Client) (embed.Embedder, error) {
	if cfg.EmbedLocal {
		return embed.NewLocal(embed.LocalConfig{Dir: cfg.ModelsDir, Model: cfg.EmbedModel})
	}
	return embed.NewOpenAI(embed.OpenAIConfig{
		BaseURL:    cfg.EmbedBaseURL,
		Model:      cfg.EmbedModel,
		Dim:        cfg.EmbedDim,
		HTTPClient: client,
	})
}

func newHTTPClient(cfg config.Config) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read GIGACHAT_CA_FILE: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("GIGACHAT_CA_FILE %s holds no PEM certificates", cfg.CAFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: cfg.HTTPTimeout, Transport: transport}, nil
}
