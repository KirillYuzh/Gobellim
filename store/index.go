package store

import (
	"encoding/json"
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// ChunksIndexBody returns the kit-chunks mapping with a dense_vector field of
// the given dims and russian-analyzed text; dims must be > 0.
func ChunksIndexBody(dims int) ([]byte, error) {
	if dims <= 0 {
		return nil, fmt.Errorf("store: vector dims must be > 0, got %d: %w", dims, core.ErrInvalidConfig)
	}
	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"doc_id":      map[string]any{"type": "keyword"},
				TenantField:   map[string]any{"type": "keyword"},
				ACLField:      map[string]any{"type": "keyword"},
				TextField:     textField(),
				VectorField:   map[string]any{"type": "dense_vector", "dims": dims, "index": true, "similarity": "cosine"},
				"position":    map[string]any{"type": "integer"},
				"source":      map[string]any{"type": "keyword"},
				"created_at":  map[string]any{"type": "date"},
				"doc_version": map[string]any{"type": "long"},
				"quarantined": map[string]any{"type": "boolean"},

				EmbedModelField: map[string]any{"type": "keyword"},
			},
		},
	}
	return json.Marshal(body)
}

// MemoryMessagesIndexBody returns the kit-memory-messages mapping; vector and
// embed_model fields are added only when dims > 0.
func MemoryMessagesIndexBody(dims int) []byte {
	props := map[string]any{
		ThreadField:    map[string]any{"type": "keyword"},
		TenantField:    map[string]any{"type": "keyword"},
		UserIDField:    map[string]any{"type": "keyword"},
		RoleField:      map[string]any{"type": "keyword"},
		ContentField:   textField(),
		UntrustedField: map[string]any{"type": "boolean"},
		"seq":          map[string]any{"type": "long"},
		"created_at":   map[string]any{"type": "date"},
	}
	if dims > 0 {
		props[VectorField] = map[string]any{"type": "dense_vector", "dims": dims, "index": true, "similarity": "cosine"}
		props[EmbedModelField] = map[string]any{"type": "keyword"}
	}
	return indexBody(props)
}

// MemoryWorkingIndexBody returns the kit-memory-working mapping, where data is
// an unindexed object.
func MemoryWorkingIndexBody() []byte {
	return indexBody(map[string]any{
		ThreadField:  map[string]any{"type": "keyword"},
		TenantField:  map[string]any{"type": "keyword"},
		UserIDField:  map[string]any{"type": "keyword"},
		"data":       map[string]any{"type": "object", "enabled": false},
		"updated_at": map[string]any{"type": "date"},
	})
}

func textField() map[string]any {
	return map[string]any{"type": "text", "analyzer": "russian"}
}

func indexBody(properties map[string]any) []byte {
	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]any{"properties": properties},
	}
	b, _ := json.Marshal(body)
	return b
}

// ILMPolicyBody returns the kit-traces-30d ILM policy: rollover at 10 GB and
// delete after days, defaulting to DefaultILMDays when days <= 0.
func ILMPolicyBody(days int) ([]byte, error) {
	if days <= 0 {
		days = DefaultILMDays
	}
	body := map[string]any{
		"policy": map[string]any{
			"phases": map[string]any{
				"hot": map[string]any{
					"actions": map[string]any{"rollover": map[string]any{"max_primary_shard_size": "10gb"}},
				},
				"delete": map[string]any{
					"min_age": fmt.Sprintf("%dd", days),
					"actions": map[string]any{"delete": map[string]any{}},
				},
			},
		},
	}
	return json.Marshal(body)
}

// TracesTemplateBody returns the kit-traces data-stream template with the trace
// mapping.
func TracesTemplateBody() []byte {
	return template(DataStreamTraces, map[string]any{
		"@timestamp":    map[string]any{"type": "date"},
		"trace_id":      map[string]any{"type": "keyword"},
		"name":          map[string]any{"type": "keyword"},
		"user_id":       map[string]any{"type": "keyword"},
		"session_id":    map[string]any{"type": "keyword"},
		"tenant":        map[string]any{"type": "keyword"},
		"input":         map[string]any{"type": "text"},
		"output":        map[string]any{"type": "text"},
		"tags":          map[string]any{"type": "keyword"},
		"kind":          map[string]any{"type": "keyword"},
		"score":         map[string]any{"type": "double"},
		"comment":       map[string]any{"type": "text"},
		"guard_version": map[string]any{"type": "keyword"},
		"guard_model":   map[string]any{"type": "keyword"},
		"policy_hash":   map[string]any{"type": "keyword"},
		"text_policy":   map[string]any{"type": "keyword"},
		"metadata":      map[string]any{"type": "object", "enabled": false},
		"usage": map[string]any{
			"properties": map[string]any{
				"prompt_tokens":     map[string]any{"type": "integer"},
				"completion_tokens": map[string]any{"type": "integer"},
				"total_tokens":      map[string]any{"type": "integer"},
				"cost":              map[string]any{"type": "double"},
			},
		},
	})
}

// ObservationsTemplateBody returns the kit-observations data-stream template
// with the observation mapping.
func ObservationsTemplateBody() []byte {
	return template(DataStreamObservations, map[string]any{
		"@timestamp":        map[string]any{"type": "date"},
		"trace_id":          map[string]any{"type": "keyword"},
		"observation_id":    map[string]any{"type": "keyword"},
		"parent_id":         map[string]any{"type": "keyword"},
		"name":              map[string]any{"type": "keyword"},
		"kind":              map[string]any{"type": "keyword"},
		"level":             map[string]any{"type": "keyword"},
		"error_kind":        map[string]any{"type": "keyword"},
		"model":             map[string]any{"type": "keyword"},
		"guard_rail":        map[string]any{"type": "keyword"},
		"guard_action":      map[string]any{"type": "keyword"},
		"guard_degraded":    map[string]any{"type": "boolean"},
		"guard_categories":  map[string]any{"type": "keyword"},
		"guard_version":     map[string]any{"type": "keyword"},
		"guard_model":       map[string]any{"type": "keyword"},
		"policy_hash":       map[string]any{"type": "keyword"},
		"text_policy":       map[string]any{"type": "keyword"},
		"tags":              map[string]any{"type": "keyword"},
		"input":             map[string]any{"type": "text"},
		"output":            map[string]any{"type": "text"},
		"status_message":    map[string]any{"type": "text"},
		"start_time":        map[string]any{"type": "date"},
		"end_time":          map[string]any{"type": "date"},
		"latency_ms":        map[string]any{"type": "double"},
		"prompt_tokens":     map[string]any{"type": "integer"},
		"completion_tokens": map[string]any{"type": "integer"},
		"total_tokens":      map[string]any{"type": "integer"},
		"cost":              map[string]any{"type": "double"},
		"metadata":          map[string]any{"type": "object", "enabled": false},
	})
}

func template(name string, properties map[string]any) []byte {
	body := map[string]any{
		"index_patterns": []string{name},
		"data_stream":    map[string]any{},
		"template": map[string]any{
			"settings": map[string]any{
				"number_of_shards":   1,
				"number_of_replicas": 0,
				"lifecycle":          map[string]any{"name": ILMPolicyName},
			},
			"mappings": map[string]any{"properties": properties},
		},
	}
	b, _ := json.Marshal(body)
	return b
}
