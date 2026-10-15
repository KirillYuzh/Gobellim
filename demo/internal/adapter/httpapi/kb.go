package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/demo/internal/usecase"
)

type kbWriteRequest struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Text  string   `json:"text"`
	ACL   []string `json:"acl"`
}

type kbRefResponse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type kbDocResponse struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Chunks int      `json:"chunks"`
	Text   string   `json:"text,omitempty"`
	ACL    []string `json:"acl,omitempty"`
}

type kbListResponse struct {
	Documents []kbDocResponse `json:"documents"`
}

type kbSearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

type kbHitResponse struct {
	DocID    string `json:"doc_id"`
	Title    string `json:"title"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type kbSearchResponse struct {
	Hits []kbHitResponse `json:"hits"`
}

func handleKbCreate(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		var req kbWriteRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, log, err)
			return
		}
		doc := domain.KnowledgeDoc{ID: req.ID, Title: req.Title, Text: req.Text, ACL: req.ACL}
		if err := kb.Create(r.Context(), principal, doc); err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusCreated, kbRefResponse{ID: doc.ID, Title: doc.Title})
	}
}

func handleKbList(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		docs, err := kb.List(r.Context(), principal)
		if err != nil {
			fail(w, log, err)
			return
		}
		out := kbListResponse{Documents: make([]kbDocResponse, 0, len(docs))}
		for _, doc := range docs {
			out.Documents = append(out.Documents, kbDocResponse{ID: doc.ID, Title: doc.Title, Chunks: doc.Chunks})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleKbGet(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		doc, err := kb.Get(r.Context(), principal, r.PathValue("id"))
		if err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, kbDocResponse{
			ID: doc.ID, Title: doc.Title, Text: doc.Text, ACL: doc.ACL, Chunks: doc.Chunks,
		})
	}
}

func handleKbUpdate(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		var req kbWriteRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, log, err)
			return
		}
		id := r.PathValue("id")
		doc := domain.KnowledgeDoc{ID: id, Title: req.Title, Text: req.Text, ACL: req.ACL}
		if err := kb.Update(r.Context(), principal, doc); err != nil {
			fail(w, log, err)
			return
		}
		writeJSON(w, http.StatusOK, kbRefResponse{ID: id, Title: doc.Title})
	}
}

func handleKbDelete(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		if err := kb.Delete(r.Context(), principal, r.PathValue("id")); err != nil {
			fail(w, log, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleKbSearch(kb *usecase.Knowledge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalOf(w, r, log)
		if !ok {
			return
		}
		var req kbSearchRequest
		if err := decode(w, r, &req); err != nil {
			fail(w, log, err)
			return
		}
		hits, err := kb.Search(r.Context(), principal, req.Query, req.TopK)
		if err != nil {
			fail(w, log, err)
			return
		}
		out := kbSearchResponse{Hits: make([]kbHitResponse, 0, len(hits))}
		for _, hit := range hits {
			out.Hits = append(out.Hits, kbHitResponse{
				DocID: hit.DocID, Title: hit.Title, Text: hit.Text, Position: hit.Position,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
