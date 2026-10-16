package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Request struct {
	Method      string
	Path        string
	Body        []byte
	ContentType string
	Refresh     string
	Idempotent  bool
}

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

type ES interface {
	Do(ctx context.Context, req Request) (statusCode int, body []byte, err error)
}

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

func NDJSONRequest(method, path string, body []byte) Request {
	return Request{Method: method, Path: path, Body: body, ContentType: "application/x-ndjson"}
}

func RawRequest(method, path string, body []byte, contentType string) Request {
	return Request{Method: method, Path: path, Body: body, ContentType: contentType}
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
