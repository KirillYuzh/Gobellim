package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Request is one explicit Elasticsearch request. The caller's client applies
// ContentType (application/json when empty) and Refresh; kit never infers them
// from Path.
type Request struct {
	Method      string
	Path        string
	Body        []byte
	ContentType string
	Refresh     string
}

// EffectivePath returns Path with a refresh=<v> query parameter appended when
// Refresh is non-empty, using & when Path already contains ?.
func (r Request) EffectivePath() string {
	if r.Refresh == "" {
		return r.Path
	}
	sep := "?"
	if strings.Contains(r.Path, "?") {
		sep = "&"
	}
	return r.Path + sep + "refresh=" + r.Refresh
}

// ES is the single-method Elasticsearch client the store depends on; the
// implementation is supplied by the caller.
type ES interface {
	Do(ctx context.Context, req Request) (statusCode int, body []byte, err error)
}

// JSONRequest builds a Request with body marshalled as JSON and ContentType
// application/json; a nil body gives an empty body.
func JSONRequest(method, path string, body any) (Request, error) {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return Request{}, fmt.Errorf("store: encode request body: %w", err)
		}
	}
	return Request{Method: method, Path: path, Body: raw, ContentType: "application/json"}, nil
}

// NDJSONRequest builds a Request with the given newline-delimited JSON body and
// ContentType application/x-ndjson.
func NDJSONRequest(method, path string, body []byte) Request {
	return Request{Method: method, Path: path, Body: body, ContentType: "application/x-ndjson"}
}

// RawRequest builds a Request with body and contentType passed through
// unchanged.
func RawRequest(method, path string, body []byte, contentType string) Request {
	return Request{Method: method, Path: path, Body: body, ContentType: contentType}
}

const (
	// IndexChunks is the index holding document chunks: text, vector, and ACL.
	IndexChunks = "kit-chunks"
	// IndexMemoryMessages is the index holding thread messages for memory recall.
	IndexMemoryMessages = "kit-memory-messages"
	// IndexMemoryWorking is the index holding one working-memory document per thread.
	IndexMemoryWorking = "kit-memory-working"
	// DataStreamTraces is the data stream holding one document per trace and per score.
	DataStreamTraces = "kit-traces"
	// DataStreamObservations is the data stream holding spans, generations, and events.
	DataStreamObservations = "kit-observations"
	// ILMPolicyName is the ILM policy that rolls over at 10 GB and deletes after 30 days.
	ILMPolicyName = "kit-traces-30d"
	// TraceTemplateName is the index template for the trace data stream.
	TraceTemplateName = "kit-traces"
	// ObservationTemplateName is the index template for the observations data stream.
	ObservationTemplateName = "kit-observations"
)

const (
	// DefaultTopK is the default number of search results returned.
	DefaultTopK = 10
	// DefaultRRFRankConst is the default reciprocal rank fusion constant.
	DefaultRRFRankConst = 60
	// DefaultILMDays is the default trace retention period in days.
	DefaultILMDays = 30
)

const (
	// VectorField is the Elasticsearch field holding the embedding vector.
	VectorField = "vector"
	// ACLField is the Elasticsearch field holding the roles allowed to see a chunk.
	ACLField = "acl"
	// TenantField is the Elasticsearch field holding the document tenant.
	TenantField = "tenant"
	// TextField is the Elasticsearch field holding chunk text for BM25.
	TextField = "text"
	// EmbedModelField is the Elasticsearch field naming the embedding model.
	EmbedModelField = "embed_model"
	// ThreadField is the Elasticsearch field holding the memory thread id.
	ThreadField = "thread_id"
	// UserIDField is the Elasticsearch field holding the memory user id.
	UserIDField = "user_id"
	// RoleField is the Elasticsearch field holding the message role.
	RoleField = "role"
	// ContentField is the Elasticsearch field holding message content.
	ContentField = "content"
	// UntrustedField is the Elasticsearch field marking a message as untrusted.
	UntrustedField = "untrusted"
	// QuarantineField is the Elasticsearch field marking a chunk as quarantined.
	QuarantineField = "quarantined"
)
