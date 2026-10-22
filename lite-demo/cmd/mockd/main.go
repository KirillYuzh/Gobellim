// Command mockd — заглушка второго сервиса: база знаний и upstream.
//
// Код в lookup выбирается по кругу (a → b → other), а не случайно: демо
// должно воспроизводиться.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"some-project/internal/entity"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	addr := env("MOCK_ADDR", ":8081")
	srv := &http.Server{
		Addr:              addr,
		Handler:           routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Info("mockd", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("mockd", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /kb/search", handleKB)
	mux.HandleFunc("POST /upstream/lookup", handleLookup)
	return mux
}

func handleKB(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Это фикстура, а не поиск: чанки не зависят от запроса.
	writeJSON(w, http.StatusOK, map[string]any{
		"query": req.Query,
		"chunks": []entity.Chunk{
			{ID: "go-modules-mock", Text: "Go modules: `go mod init example.com/mock` создаёт go.mod (mock).", Score: 0.93, Mock: true},
			{ID: "go-goroutines-mock", Text: "Goroutines: `go f()` запускает функцию конкурентно (mock).", Score: 0.87, Mock: true},
			{ID: "go-interfaces-mock", Text: "Интерфейсы в Go реализуются неявно, без ключевого слова implements (mock).", Score: 0.81, Mock: true},
			{ID: "go-testing-mock", Text: "`go test ./...` запускает тесты всех пакетов модуля (mock).", Score: 0.76, Mock: true},
		},
	})
}

var lookupSeq atomic.Uint64

func handleLookup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
	}
	if !decode(w, r, &req) {
		return
	}
	codes := [...]string{"a", "b", "other"}
	code := codes[(lookupSeq.Add(1)-1)%uint64(len(codes))]
	out := map[string]any{"code": code, "query": req.Query, "mock": true}
	switch code {
	case "a":
		out["body"] = "A: mock-факт: `go build ./...` собирает все пакеты модуля (mock)."
	case "b":
		out["body"] = "B: mock-факт: `go test ./...` запускает тесты всех пакетов (mock)."
		out["question"] = "Уточните, пожалуйста, версию Go (mock)."
	default:
		out["error"] = "upstream returned an unknown code (mock)"
	}
	writeJSON(w, http.StatusOK, out)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
