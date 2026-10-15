package store

import (
	"encoding/json"
	"fmt"

	"github.com/gobellim/kit/internal/core"
)

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
				TextField:     map[string]any{"type": "text"},
				VectorField:   map[string]any{"type": "dense_vector", "dims": dims, "index": true, "similarity": "cosine"},
				"position":    map[string]any{"type": "integer"},
				"source":      map[string]any{"type": "keyword"},
				"created_at":  map[string]any{"type": "date"},
				"quarantined": map[string]any{"type": "boolean"},
			},
		},
	}
	return json.Marshal(body)
}

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

func TracesTemplateBody() []byte {
	return template(DataStreamTraces, map[string]any{
		"@timestamp": map[string]any{"type": "date"},
		"trace_id":   map[string]any{"type": "keyword"},
		"name":       map[string]any{"type": "keyword"},
		"user_id":    map[string]any{"type": "keyword"},
		"session_id": map[string]any{"type": "keyword"},
		"tenant":     map[string]any{"type": "keyword"},
		"input":      map[string]any{"type": "text"},
		"output":     map[string]any{"type": "text"},
		"tags":       map[string]any{"type": "keyword"},
		"metadata":   map[string]any{"type": "object", "enabled": false},
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
		"guard_categories":  map[string]any{"type": "keyword"},
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
