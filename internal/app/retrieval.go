package app

import (
	"context"
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// DefaultRetrievalToolName is the tool name used when RetrievalTool is called
// with an empty name.
const DefaultRetrievalToolName = "search_knowledge"

// RetrievalIn is the typed input of the retrieval tool; a non-positive TopK uses
// the search default.
type RetrievalIn struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

// RetrievalHit is one passage returned by the retrieval tool.
type RetrievalHit struct {
	DocID    string `json:"doc_id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

// RetrievalOut is the retrieval tool's result: matching passages in rank order.
type RetrievalOut struct {
	Hits []RetrievalHit `json:"hits"`
}

// RetrievalTool declares a typed tool that calls App.Search, so the tool
// permission is checked first and Search then applies the search permission and
// the caller's ACL. It needs no App at declaration time: the App is taken from
// the call context, and the tool fails with ErrUnauthorized when the context
// carries no principal.
func RetrievalTool(name string, opts ...Option) ToolDef {
	if name == "" {
		name = DefaultRetrievalToolName
	}

	all := append([]Option{
		Desc("Search the knowledge base and return the matching passages. Use it whenever the answer may be in the documents."),
	}, opts...)

	return Tool(name, func(ctx context.Context, in RetrievalIn) (RetrievalOut, error) {
		if _, ok := core.PrincipalFrom(ctx); !ok {
			return RetrievalOut{}, fmt.Errorf("gobellim: retrieval tool %s: no principal: %w", name, core.ErrUnauthorized)
		}
		app := appFromContext(ctx)
		if app == nil {
			return RetrievalOut{}, fmt.Errorf("gobellim: retrieval tool %s: no App in context: %w", name, core.ErrInvalidConfig)
		}
		hits, err := app.Search(ctx, SearchRequest{Query: in.Query, TopK: in.TopK})
		if err != nil {
			return RetrievalOut{}, err
		}
		out := RetrievalOut{Hits: make([]RetrievalHit, 0, len(hits))}
		for _, h := range hits {
			out.Hits = append(out.Hits, RetrievalHit{DocID: h.DocID, Text: h.Text, Position: h.Position})
		}
		return out, nil
	}, all...)
}
