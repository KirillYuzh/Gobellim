// Package store builds and parses Elasticsearch payloads; the ES client is
// supplied by the caller.
package store

import (
	"context"
	"io"
)

// ES is the Elasticsearch client the caller provides.
type ES interface {
	Do(ctx context.Context, method, path string, body io.Reader) (statusCode int, respBody []byte, err error)
}

const (
	IndexChunks             = "kit-chunks"
	DataStreamTraces        = "kit-traces"
	DataStreamObservations  = "kit-observations"
	ILMPolicyName           = "kit-traces-30d"
	TraceTemplateName       = "kit-traces"
	ObservationTemplateName = "kit-observations"
)

const (
	DefaultTopK         = 10
	DefaultRRFRankConst = 60
	DefaultILMDays      = 30
)

const (
	VectorField = "vector"
	ACLField    = "acl"
	TenantField = "tenant"
	TextField   = "text"
)
