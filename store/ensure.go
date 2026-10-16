package store

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

var EnsureStepTimeout = 30 * time.Second

type step struct {
	method      string
	path        string
	contentType string
	body        []byte
}

func EnsureSchema(ctx context.Context, es ES, dims int) error {
	ilm, err := ILMPolicyBody(DefaultILMDays)
	if err != nil {
		return err
	}

	steps := []step{
		{http.MethodPut, "/_ilm/policy/" + ILMPolicyName, "application/json", ilm},
		{http.MethodPut, "/_index_template/" + TraceTemplateName, "application/json", TracesTemplateBody()},
		{http.MethodPut, "/_index_template/" + ObservationTemplateName, "application/json", ObservationsTemplateBody()},
		{http.MethodPut, "/" + IndexMemoryMessages, "application/json", MemoryMessagesIndexBody(dims)},
		{http.MethodPut, "/" + IndexMemoryWorking, "application/json", MemoryWorkingIndexBody()},
	}
	if dims > 0 {
		chunks, err := ChunksIndexBody(dims)
		if err != nil {
			return err
		}
		steps = append(steps, step{http.MethodPut, "/" + IndexChunks, "application/json", chunks})
	}
	for _, s := range steps {
		req := RawRequest(s.method, s.path, s.body, s.contentType)
		stepCtx, cancel := context.WithTimeout(ctx, EnsureStepTimeout)
		status, body, err := es.Do(stepCtx, req)
		cancel()
		if err != nil {
			return fmt.Errorf("store: bootstrap %s: %w", req.EffectivePath(), err)
		}
		if status >= 200 && status < 300 {
			continue
		}
		if alreadyExists(body) {
			continue
		}
		return fmt.Errorf("store: bootstrap %s: status %d: %s: %w",
			req.EffectivePath(), status, Snippet(body), core.ErrProvider)
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
