package store

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type step struct {
	method string
	path   string
	body   []byte
}

// EnsureSchema creates the ILM policy, the trace/observation data stream
// templates and, when dims is positive, the chunks index. It is idempotent: an
// "already exists" response is treated as success.
func EnsureSchema(ctx context.Context, es ES, dims int) error {
	ilm, err := ILMPolicyBody(DefaultILMDays)
	if err != nil {
		return err
	}

	steps := []step{
		{http.MethodPut, "/_ilm/policy/" + ILMPolicyName, ilm},
		{http.MethodPut, "/_index_template/" + TraceTemplateName, TracesTemplateBody()},
		{http.MethodPut, "/_index_template/" + ObservationTemplateName, ObservationsTemplateBody()},
	}
	if dims > 0 {
		chunks, err := ChunksIndexBody(dims)
		if err != nil {
			return err
		}
		steps = append(steps, step{http.MethodPut, "/" + IndexChunks, chunks})
	}
	for _, s := range steps {
		status, body, err := es.Do(ctx, s.method, s.path, strings.NewReader(string(s.body)))
		if err != nil {
			return fmt.Errorf("store: bootstrap %s: %w", s.path, err)
		}
		if status >= 200 && status < 300 {
			continue
		}
		if alreadyExists(body) {
			continue
		}
		return fmt.Errorf("store: bootstrap %s: status %d: %s", s.path, status, Snippet(body))
	}
	return nil
}

func alreadyExists(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "resource_already_exists_exception") ||
		strings.Contains(s, "already_exists_exception")
}

func Snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
