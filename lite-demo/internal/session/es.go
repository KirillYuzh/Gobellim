package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// defaultIndex is where suspended runs live.
	defaultIndex = "lite-demo-runs"
	// esTimeout bounds one request when ESConfig.Client is nil.
	esTimeout = 10 * time.Second
	// maxESBody caps one response body read.
	maxESBody = 4 << 20
)

// indexMapping indexes only what Load filters on. The result is stored in
// _source but not indexed: the store retrieves runs by id, never searches them.
const indexMapping = `{"mappings":{"dynamic":false,"properties":{` +
	`"id":{"type":"keyword"},` +
	`"principal":{"properties":{` +
	`"user_id":{"type":"keyword"},"tenant":{"type":"keyword"},"chat_id":{"type":"keyword"}}},` +
	`"expires":{"type":"date"},` +
	`"resume_id":{"type":"keyword"}}}}`

// ESConfig configures the Elasticsearch run store. URL is required and may
// carry a path prefix.
type ESConfig struct {
	URL    string
	Index  string
	TTL    time.Duration
	Client *http.Client
}

// ESStore keeps runs in Elasticsearch so a suspended run survives a restart and
// is visible to another instance. Concurrency is optimistic: Save sends the
// sequence number it loaded, and a concurrent writer turns into ErrConflict
// instead of an overwrite.
type ESStore[S any] struct {
	base  *url.URL
	index string
	hc    *http.Client
	ttl   time.Duration
}

// NewESStore validates the configuration, ensures the index exists and returns
// the store. A malformed URL or an unreachable cluster is a startup error, not
// a surprise on the first resume.
func NewESStore[S any](cfg ESConfig) (*ESStore[S], error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("session: elasticsearch URL is required")
	}
	base, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("session: elasticsearch URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("session: elasticsearch URL %q must be absolute", cfg.URL)
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = ""
	index := cfg.Index
	if index == "" {
		index = defaultIndex
	}
	if strings.ContainsAny(index, `/\ `) {
		return nil, fmt.Errorf("session: elasticsearch index %q is invalid", index)
	}
	hc := cfg.Client
	if hc == nil {
		hc = &http.Client{Timeout: esTimeout}
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	s := &ESStore[S]{base: base, index: index, hc: hc, ttl: ttl}
	ctx, cancel := context.WithTimeout(context.Background(), esTimeout)
	defer cancel()
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Expiry returns the expiration time of a run created now.
func (s *ESStore[S]) Expiry() time.Time { return time.Now().Add(s.ttl) }

// Load returns a run to its owner. An expired or foreign run is ErrNotFound.
func (s *ESStore[S]) Load(ctx context.Context, runID string, p Principal) (*Run[S], error) {
	if runID == "" {
		return nil, fmt.Errorf("%w: run id is required", ErrInvalid)
	}
	status, body, err := s.do(ctx, http.MethodGet, s.docPath(runID), nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("session: elasticsearch: load %s: status %d", runID, status)
	}
	var doc struct {
		SeqNo       int64           `json:"_seq_no"`
		PrimaryTerm int64           `json:"_primary_term"`
		Source      json.RawMessage `json:"_source"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("session: elasticsearch: decode %s: %w", runID, err)
	}
	var run Run[S]
	if err := json.Unmarshal(doc.Source, &run); err != nil {
		return nil, fmt.Errorf("session: elasticsearch: decode run %s: %w", runID, err)
	}
	if time.Now().After(run.Expires) {
		return nil, ErrNotFound
	}
	if !run.Principal.matches(p) {
		return nil, ErrNotFound
	}
	run.seq, run.term = doc.SeqNo, doc.PrimaryTerm
	return &run, nil
}

// Save writes the run. The first Save creates the document; a Save after Load
// updates it only if nobody else changed it.
func (s *ESStore[S]) Save(ctx context.Context, run *Run[S]) error {
	if run == nil || run.ID == "" {
		return fmt.Errorf("%w: run id is required", ErrInvalid)
	}
	body, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("session: elasticsearch: encode run %s: %w", run.ID, err)
	}
	path := s.docPath(run.ID)
	if run.seq == 0 {
		path += "?op_type=create&refresh=false"
	} else {
		path += fmt.Sprintf("?if_seq_no=%d&if_primary_term=%d&refresh=false", run.seq, run.term)
	}
	status, resp, err := s.do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
		var doc struct {
			SeqNo       int64 `json:"_seq_no"`
			PrimaryTerm int64 `json:"_primary_term"`
		}
		if err := json.Unmarshal(resp, &doc); err == nil {
			run.seq, run.term = doc.SeqNo, doc.PrimaryTerm
		}
		return nil
	case http.StatusConflict:
		return fmt.Errorf("%w: run %s", ErrConflict, run.ID)
	default:
		return fmt.Errorf("session: elasticsearch: save %s: status %d", run.ID, status)
	}
}

// ensure creates the index on the first start and accepts an existing one.
func (s *ESStore[S]) ensure(ctx context.Context) error {
	status, body, err := s.do(ctx, http.MethodGet, "/"+s.index, nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
	default:
		return fmt.Errorf("session: elasticsearch: index %s: status %d", s.index, status)
	}
	status, body, err = s.do(ctx, http.MethodPut, "/"+s.index, []byte(indexMapping))
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("session: elasticsearch: create index %s: status %d: %s",
			s.index, status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *ESStore[S]) docPath(runID string) string {
	return "/" + s.index + "/_doc/" + url.PathEscape(runID)
}

func (s *ESStore[S]) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return 0, nil, fmt.Errorf("session: elasticsearch: parse path %q: %w", path, err)
	}
	u := *s.base
	u.Path = s.base.Path + rel.Path
	u.RawPath = ""
	u.RawQuery = rel.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("session: elasticsearch: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("session: elasticsearch: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxESBody+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("session: elasticsearch: read %s: %w", path, err)
	}
	if len(data) > maxESBody {
		return resp.StatusCode, nil, fmt.Errorf("session: elasticsearch: response exceeds %d bytes", maxESBody)
	}
	return resp.StatusCode, data, nil
}
