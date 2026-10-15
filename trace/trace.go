// Package trace writes Langfuse-shaped traces to Elasticsearch.
package trace

import "time"

type Kind string

const (
	KindSpan       Kind = "SPAN"
	KindGeneration Kind = "GENERATION"
	KindEvent      Kind = "EVENT"
)

type Level string

const (
	LevelDefault Level = "DEFAULT"
	LevelError   Level = "ERROR"
)

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

type Observation struct {
	ID              string
	TraceID         string
	ParentID        string
	Name            string
	Kind            Kind
	Level           Level
	ErrorKind       string
	Model           string
	Input           string
	Output          string
	StatusMessage   string
	GuardRail       string
	GuardAction     string
	GuardCategories []string
	StartTime       time.Time
	EndTime         time.Time
	Usage           Usage
	Metadata        map[string]any
}

type Trace struct {
	ID        string
	Name      string
	UserID    string
	SessionID string
	Tenant    string
	Input     string
	Output    string
	Tags      []string
	StartTime time.Time
	EndTime   time.Time
	Usage     Usage
	Metadata  map[string]any
}

type Score struct {
	TraceID   string
	Name      string
	Value     float64
	Comment   string
	Timestamp time.Time
}

func (t Trace) document() map[string]any {
	start := t.StartTime
	if start.IsZero() {
		start = time.Now().UTC()
	}
	end := t.EndTime
	if end.IsZero() {
		end = start
	}
	return map[string]any{
		"@timestamp": end.UTC().Format(time.RFC3339Nano),
		"trace_id":   t.ID,
		"name":       t.Name,
		"user_id":    t.UserID,
		"session_id": t.SessionID,
		"tenant":     t.Tenant,
		"input":      t.Input,
		"output":     t.Output,
		"tags":       t.Tags,
		"metadata":   t.Metadata,
		"usage": map[string]any{
			"prompt_tokens":     t.Usage.PromptTokens,
			"completion_tokens": t.Usage.CompletionTokens,
			"total_tokens":      t.Usage.TotalTokens,
			"cost":              t.Usage.Cost,
		},
	}
}

func (o Observation) document() map[string]any {
	start := o.StartTime
	if start.IsZero() {
		start = time.Now().UTC()
	}
	end := o.EndTime
	if end.IsZero() {
		end = start
	}
	level := o.Level
	if level == "" {
		level = LevelDefault
	}
	doc := map[string]any{
		"@timestamp":        end.UTC().Format(time.RFC3339Nano),
		"trace_id":          o.TraceID,
		"observation_id":    o.ID,
		"parent_id":         o.ParentID,
		"name":              o.Name,
		"kind":              string(o.Kind),
		"level":             string(level),
		"model":             o.Model,
		"input":             o.Input,
		"output":            o.Output,
		"status_message":    o.StatusMessage,
		"start_time":        start.UTC().Format(time.RFC3339Nano),
		"end_time":          end.UTC().Format(time.RFC3339Nano),
		"latency_ms":        float64(end.Sub(start)) / float64(time.Millisecond),
		"prompt_tokens":     o.Usage.PromptTokens,
		"completion_tokens": o.Usage.CompletionTokens,
		"total_tokens":      o.Usage.TotalTokens,
		"cost":              o.Usage.Cost,
		"metadata":          o.Metadata,
	}
	if o.ErrorKind != "" {
		doc["error_kind"] = o.ErrorKind
	}
	if o.GuardRail != "" {
		doc["guard_rail"] = o.GuardRail
	}
	if o.GuardAction != "" {
		doc["guard_action"] = o.GuardAction
	}
	if len(o.GuardCategories) > 0 {
		doc["guard_categories"] = o.GuardCategories
	}
	return doc
}
