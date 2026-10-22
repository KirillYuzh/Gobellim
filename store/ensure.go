package store

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// ensureStepTimeout bounds each individual schema-creation request made by
// EnsureSchema.
const ensureStepTimeout = 30 * time.Second

type step struct {
	method      string
	path        string
	contentType string
	body        []byte
}

// EnsureSchema creates the ILM policy, data-stream templates, memory indexes,
// and, when dims > 0, the chunks index. Already-exists responses count as
// success, and non-2xx responses wrap core.ErrProvider.
func EnsureSchema(ctx context.Context, es ES, dims int) error {
	ilm, err := ILMPolicyBody(DefaultILMDays)
	if err != nil {
		return err
	}

	memMsgBody, err := MemoryMessagesIndexBody(dims)
	if err != nil {
		return err
	}
	memWorkBody, err := MemoryWorkingIndexBody()
	if err != nil {
		return err
	}
	tracesTemplate, err := TracesTemplateBody()
	if err != nil {
		return err
	}
	observationsTemplate, err := ObservationsTemplateBody()
	if err != nil {
		return err
	}

	steps := []step{
		{http.MethodPut, "/_ilm/policy/" + ILMPolicyName, "application/json", ilm},
		{http.MethodPut, "/_index_template/" + TraceTemplateName, "application/json", tracesTemplate},
		{http.MethodPut, "/_index_template/" + ObservationTemplateName, "application/json", observationsTemplate},
		{http.MethodPut, "/" + IndexMemoryMessages, "application/json", memMsgBody},
		{http.MethodPut, "/" + IndexMemoryWorking, "application/json", memWorkBody},
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
		stepCtx, cancel := context.WithTimeout(ctx, ensureStepTimeout)
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
