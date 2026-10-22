package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeES serves the two document endpoints the store uses, with the same
// optimistic concurrency semantics as Elasticsearch. It is an http.RoundTripper
// so the tests need no listening port.
type fakeES struct {
	mu   sync.Mutex
	docs map[string]fakeDoc
}

type fakeDoc struct {
	source string
	seq    int64
}

func newFakeES() *fakeES {
	return &fakeES{docs: map[string]fakeDoc{}}
}

func (f *fakeES) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.serve(rec, r)
	return rec.Result(), nil
}

func (f *fakeES) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"index":"ok"}`))
	case len(parts) == 1 && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	case len(parts) == 3 && parts[1] == "_doc":
		id := parts[2]
		switch r.Method {
		case http.MethodGet:
			doc, ok := f.docs[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"found":false}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"_seq_no":` + strconv.FormatInt(doc.seq, 10) +
				`,"_primary_term":1,"_source":` + doc.source + `}`))
		case http.MethodPut:
			f.put(w, r, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeES) put(w http.ResponseWriter, r *http.Request, id string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	doc, exists := f.docs[id]
	q := r.URL.Query()
	if q.Get("op_type") == "create" && exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if want := q.Get("if_seq_no"); want != "" {
		seq, err := strconv.ParseInt(want, 10, 64)
		if err != nil || !exists || seq != doc.seq {
			w.WriteHeader(http.StatusConflict)
			return
		}
	}
	next := doc.seq + 1
	f.docs[id] = fakeDoc{source: string(body), seq: next}
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"_seq_no":` + strconv.FormatInt(next, 10) + `,"_primary_term":1}`))
}

func newES(t *testing.T) *ESStore[state] {
	t.Helper()
	store, err := NewESStore[state](ESConfig{
		URL:    "http://es.test",
		TTL:    time.Minute,
		Client: &http.Client{Transport: newFakeES()},
	})
	if err != nil {
		t.Fatalf("NewESStore: %v", err)
	}
	return store
}

func TestESStoreRoundTrip(t *testing.T) {
	store := newES(t)
	run := &Run[state]{
		ID:        "run-1",
		Principal: owner,
		Expires:   time.Now().Add(time.Minute),
		Last:      Result[state]{Status: StatusAwaitingUser, RunID: "run-1", Question: &Question{Stage: "param"}},
	}
	if err := store.Save(context.Background(), run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if run.seq == 0 {
		t.Fatal("Save did not record the sequence number")
	}

	got, err := store.Load(context.Background(), "run-1", owner)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != "run-1" || got.Last.Question == nil || got.Last.Question.Stage != "param" {
		t.Fatalf("load = %+v", got)
	}
	if got.seq == 0 {
		t.Fatal("Load did not return the sequence number")
	}

	if _, err := store.Load(context.Background(), "run-1", Principal{UserID: "u-2", Tenant: "acme"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign load = %v, want ErrNotFound", err)
	}
	if _, err := store.Load(context.Background(), "missing", owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing load = %v, want ErrNotFound", err)
	}
}

func TestESStoreExpiresRuns(t *testing.T) {
	store := newES(t)
	run := &Run[state]{
		ID:        "run-old",
		Principal: owner,
		Expires:   time.Now().Add(-time.Minute),
		Last:      Result[state]{Status: StatusAwaitingUser, RunID: "run-old"},
	}
	if err := store.Save(context.Background(), run); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Load(context.Background(), "run-old", owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load = %v, want ErrNotFound for an expired run", err)
	}
}

func TestESStoreRejectsStaleSave(t *testing.T) {
	store := newES(t)
	run := &Run[state]{
		ID:        "run-1",
		Principal: owner,
		Expires:   time.Now().Add(time.Minute),
		Last:      Result[state]{Status: StatusAwaitingUser, RunID: "run-1"},
	}
	if err := store.Save(context.Background(), run); err != nil {
		t.Fatalf("create: %v", err)
	}
	a, err := store.Load(context.Background(), "run-1", owner)
	if err != nil {
		t.Fatalf("load a: %v", err)
	}
	b, err := store.Load(context.Background(), "run-1", owner)
	if err != nil {
		t.Fatalf("load b: %v", err)
	}

	a.Last.State.Param = "a"
	if err := store.Save(context.Background(), a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	b.Last.State.Param = "b"
	if err := store.Save(context.Background(), b); !errors.Is(err, ErrConflict) {
		t.Fatalf("save b = %v, want ErrConflict", err)
	}
}

// TestSessionWithESStore runs the scenario over Elasticsearch and resumes it
// through a second Session: the execution is rebuilt from the stored state.
func TestSessionWithESStore(t *testing.T) {
	store := newES(t)
	first := newScenario(store)
	start := startScenario(t, first)
	if start.Question == nil || start.Question.Stage != "param" {
		t.Fatalf("start = %+v", start)
	}

	second := newScenario(store)
	next := resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Text: "go modules"})
	if next.Question == nil || next.Question.Stage != "path" {
		t.Fatalf("resume = %+v", next)
	}
	branch := resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Choice: "B"})
	if branch.Question == nil || branch.Question.Stage != "topic" {
		t.Fatalf("branch = %+v", branch)
	}
	done := resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Choice: "X"})
	if done.Status != StatusCompleted || done.State.Result != "B:X" {
		t.Fatalf("done = %+v", done)
	}
}

func TestESStoreEncodesRun(t *testing.T) {
	run := &Run[state]{
		ID:        "run-1",
		Principal: Principal{UserID: "u-1", Tenant: "acme", ChatID: "c-1"},
		Last:      Result[state]{Status: StatusCompleted, RunID: "run-1", State: state{Result: "done"}},
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"user_id":"u-1"`, `"chat_id":"c-1"`, `"Result":"done"`, `"status":"completed"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("encoded run = %s, want %s", raw, want)
		}
	}
}
