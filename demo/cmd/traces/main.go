// Command traces reads kit traces, observations and scores from Elasticsearch.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gobellim/kit/demo/internal/adapter/elastic"
	"github.com/gobellim/kit/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "traces: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("traces", flag.ContinueOnError)
	url := fs.String("url", envOr("KIT_ES_URL", "http://localhost:9200"), "Elasticsearch base URL")
	limit := fs.Int("limit", 20, "how many traces to list")
	name := fs.String("name", "", "filter by trace name (chat, ingest, search, generate, memory)")
	tenant := fs.String("tenant", "", "filter by tenant")
	id := fs.String("trace", "", "show one trace by id, with its observations and scores")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	es := elastic.New(*url, &http.Client{Timeout: 20 * time.Second})

	if *id != "" {
		return showTrace(ctx, es, *id)
	}
	return listTraces(ctx, es, *limit, *name, *tenant)
}

type traceDoc struct {
	Timestamp string  `json:"@timestamp"`
	TraceID   string  `json:"trace_id"`
	Name      string  `json:"name"`
	Tenant    string  `json:"tenant"`
	UserID    string  `json:"user_id"`
	SessionID string  `json:"session_id"`
	Kind      string  `json:"kind"`
	Score     float64 `json:"score"`
	Comment   string  `json:"comment"`
	Usage     struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
	TextPolicy string `json:"text_policy"`
}

type obsDoc struct {
	StartTime        string   `json:"start_time"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Model            string   `json:"model"`
	LatencyMS        float64  `json:"latency_ms"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	Cost             float64  `json:"cost"`
	ErrorKind        string   `json:"error_kind"`
	GuardRail        string   `json:"guard_rail"`
	GuardAction      string   `json:"guard_action"`
	GuardCategories  []string `json:"guard_categories"`
	StatusMessage    string   `json:"status_message"`
}

type hit struct {
	Source json.RawMessage `json:"_source"`
}

type searchResponse struct {
	Hits struct {
		Hits []hit `json:"hits"`
	} `json:"hits"`
}

func listTraces(ctx context.Context, es store.ES, limit int, name, tenant string) error {
	if limit < 1 {
		limit = 1
	}
	filter := []any{
		map[string]any{"bool": map[string]any{"must_not": []any{
			map[string]any{"term": map[string]any{"kind": "SCORE"}},
		}}},
	}
	if name != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"name": name}})
	}
	if tenant != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"tenant": tenant}})
	}
	body := map[string]any{
		"size":  limit,
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"sort":  []any{map[string]any{"@timestamp": map[string]any{"order": "desc"}}},
	}

	var res searchResponse
	if err := search(ctx, es, store.DataStreamTraces, body, &res); err != nil {
		return err
	}
	if len(res.Hits.Hits) == 0 {
		fmt.Println("no traces")
		return nil
	}
	fmt.Printf("%-19s %-12s %-8s %-10s %-12s %8s %9s\n", "time", "trace", "name", "tenant", "user", "tokens", "cost")
	for _, h := range res.Hits.Hits {
		var t traceDoc
		if err := json.Unmarshal(h.Source, &t); err != nil {
			continue
		}
		fmt.Printf("%-19s %-12s %-8s %-10s %-12s %8d %9.4f\n",
			formatTime(t.Timestamp), shortID(t.TraceID), t.Name, t.Tenant, t.UserID, t.Usage.TotalTokens, t.Usage.Cost)
	}
	fmt.Println("\ntraces -trace <id> shows one trace in full")
	return nil
}

func showTrace(ctx context.Context, es store.ES, id string) error {
	trace, err := traceByID(ctx, es, id)
	if err != nil {
		return err
	}
	if trace == nil {
		return fmt.Errorf("trace %s not found", id)
	}
	fmt.Printf("trace %s  %s  %s\n", trace.TraceID, trace.Name, formatTime(trace.Timestamp))
	fmt.Printf("  tenant  %s\n  user    %s\n", trace.Tenant, trace.UserID)
	if trace.SessionID != "" {
		fmt.Printf("  session %s\n", trace.SessionID)
	}
	fmt.Printf("  tokens  %d (prompt %d, completion %d)\n", trace.Usage.TotalTokens, trace.Usage.PromptTokens, trace.Usage.CompletionTokens)
	fmt.Printf("  cost    %.4f\n  text    %s\n", trace.Usage.Cost, trace.TextPolicy)

	if scores, err := scoresByTraceID(ctx, es, id); err == nil && len(scores) > 0 {
		fmt.Println("  scores")
		for _, s := range scores {
			fmt.Printf("    %-16s %.3f  %s\n", s.Name, s.Score, s.Comment)
		}
	}

	obs, err := observationsByTraceID(ctx, es, id)
	if err != nil {
		return err
	}
	if len(obs) == 0 {
		fmt.Println("  observations: none")
		return nil
	}
	fmt.Printf("  observations (%d)\n", len(obs))
	for _, o := range obs {
		fmt.Printf("    %-10s %-24s %7.1fms %7d tok %8.4f%s%s\n",
			o.Kind, o.Name, o.LatencyMS, o.PromptTokens+o.CompletionTokens, o.Cost, guardTag(o), errorTag(o))
	}
	return nil
}

func traceByID(ctx context.Context, es store.ES, id string) (*traceDoc, error) {
	body := map[string]any{
		"size":  1,
		"query": traceFilter(id),
	}
	var res searchResponse
	if err := search(ctx, es, store.DataStreamTraces, body, &res); err != nil {
		return nil, err
	}
	if len(res.Hits.Hits) == 0 {
		return nil, nil
	}
	var t traceDoc
	if err := json.Unmarshal(res.Hits.Hits[0].Source, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func scoresByTraceID(ctx context.Context, es store.ES, id string) ([]traceDoc, error) {
	body := map[string]any{
		"size": 100,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"trace_id": id}},
			map[string]any{"term": map[string]any{"kind": "SCORE"}},
		}}},
	}
	var res searchResponse
	if err := search(ctx, es, store.DataStreamTraces, body, &res); err != nil {
		return nil, err
	}
	out := make([]traceDoc, 0, len(res.Hits.Hits))
	for _, h := range res.Hits.Hits {
		var s traceDoc
		if err := json.Unmarshal(h.Source, &s); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func observationsByTraceID(ctx context.Context, es store.ES, id string) ([]obsDoc, error) {
	body := map[string]any{
		"size":  500,
		"query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"trace_id": id}}}}},
		"sort":  []any{map[string]any{"start_time": map[string]any{"order": "asc", "unmapped_type": "date"}}},
	}
	var res searchResponse
	if err := search(ctx, es, store.DataStreamObservations, body, &res); err != nil {
		return nil, err
	}
	out := make([]obsDoc, 0, len(res.Hits.Hits))
	for _, h := range res.Hits.Hits {
		var o obsDoc
		if err := json.Unmarshal(h.Source, &o); err != nil {
			continue
		}
		out = append(out, o)
	}
	return out, nil
}

func traceFilter(id string) map[string]any {
	return map[string]any{"bool": map[string]any{"filter": []any{
		map[string]any{"term": map[string]any{"trace_id": id}},
		map[string]any{"bool": map[string]any{"must_not": []any{map[string]any{"term": map[string]any{"kind": "SCORE"}}}}},
	}}}
}

func search(ctx context.Context, es store.ES, index string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode query: %w", err)
	}
	status, resp, err := es.Do(ctx, store.RawRequest(http.MethodPost, "/"+index+"/_search", raw, "application/json"))
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elasticsearch %s: status %d: %s", index, status, strings.TrimSpace(string(resp)))
	}
	if err := json.Unmarshal(resp, out); err != nil {
		return fmt.Errorf("decode %s response: %w", index, err)
	}
	return nil
}

func guardTag(o obsDoc) string {
	if o.GuardRail == "" && o.GuardAction == "" && len(o.GuardCategories) == 0 {
		return ""
	}
	return fmt.Sprintf("  guard=%s/%s%s", o.GuardRail, o.GuardAction, categories(o.GuardCategories))
}

func categories(cats []string) string {
	if len(cats) == 0 {
		return ""
	}
	return "[" + strings.Join(cats, ",") + "]"
}

func errorTag(o obsDoc) string {
	if o.ErrorKind == "" {
		return ""
	}
	return fmt.Sprintf("  error=%s", o.ErrorKind)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func formatTime(v string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Local().Format("2006-01-02 15:04:05")
		}
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
