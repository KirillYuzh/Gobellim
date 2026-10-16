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
	Tags            []string
	StartTime       time.Time
	EndTime         time.Time
	Usage           Usage
	Metadata        map[string]any

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   TextPolicy

	// GuardDegraded records that an optional guard layer was unavailable and the
	// verdict was produced by the remaining layers.
	GuardDegraded bool
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

	GuardVersion string
	GuardModel   string
	PolicyHash   string
	TextPolicy   TextPolicy
}

type Score struct {
	TraceID   string
	Name      string
	Value     float64
	Comment   string
	Timestamp time.Time
}

func usageDoc(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":     u.PromptTokens,
		"completion_tokens": u.CompletionTokens,
		"total_tokens":      u.TotalTokens,
		"cost":              u.Cost,
	}
}

func putAudit(doc map[string]any, version, model, policyHash string) {
	if version != "" {
		doc["guard_version"] = version
	}
	if model != "" {
		doc["guard_model"] = model
	}
	if policyHash != "" {
		doc["policy_hash"] = policyHash
	}
}

func (t Trace) document(cfg exportConfig) map[string]any {
	start := t.StartTime
	if start.IsZero() {
		start = time.Now().UTC()
	}
	end := t.EndTime
	if end.IsZero() {
		end = start
	}

	input := cfg.clip(t.Input)
	output := cfg.clip(t.Output)
	if cfg.hideInput {
		input = ""
	}
	if cfg.hideOutput {
		output = ""
	}

	doc := map[string]any{
		"@timestamp":  end.UTC().Format(time.RFC3339Nano),
		"trace_id":    t.ID,
		"name":        t.Name,
		"user_id":     t.UserID,
		"session_id":  t.SessionID,
		"tenant":      t.Tenant,
		"input":       input,
		"output":      output,
		"metadata":    cfg.truncate(t.Metadata),
		"usage":       usageDoc(t.Usage),
		"text_policy": t.TextPolicy.String(),
	}
	if len(t.Tags) > 0 {
		doc["tags"] = t.Tags
	}
	putAudit(doc, t.GuardVersion, t.GuardModel, t.PolicyHash)
	return doc
}

func (o Observation) document(cfg exportConfig) map[string]any {
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

	input := cfg.clip(o.Input)
	output := cfg.clip(o.Output)
	if cfg.hideInput {
		input = ""
	}
	if cfg.hideOutput {
		output = ""
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
		"input":             input,
		"output":            output,
		"status_message":    cfg.clip(o.StatusMessage),
		"start_time":        start.UTC().Format(time.RFC3339Nano),
		"end_time":          end.UTC().Format(time.RFC3339Nano),
		"latency_ms":        float64(end.Sub(start)) / float64(time.Millisecond),
		"prompt_tokens":     o.Usage.PromptTokens,
		"completion_tokens": o.Usage.CompletionTokens,
		"total_tokens":      o.Usage.TotalTokens,
		"cost":              o.Usage.Cost,
		"metadata":          cfg.truncate(o.Metadata),
		"text_policy":       o.TextPolicy.String(),
	}
	if len(o.Tags) > 0 {
		doc["tags"] = o.Tags
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
	if o.GuardDegraded {
		doc["guard_degraded"] = true
	}
	if len(o.GuardCategories) > 0 {
		doc["guard_categories"] = o.GuardCategories
	}
	putAudit(doc, o.GuardVersion, o.GuardModel, o.PolicyHash)
	return doc
}
