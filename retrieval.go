package kit

import (
	"context"
	"fmt"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

const DefaultRetrievalToolName = "search_knowledge"

type RetrievalIn struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

type RetrievalHit struct {
	DocID    string `json:"doc_id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}

type RetrievalOut struct {
	Hits []RetrievalHit `json:"hits"`
}

func RetrievalTool(app *App, name string, opts ...ToolOption) (*ToolDef, error) {
	if app == nil {
		return nil, fmt.Errorf("kit: retrieval tool: app is required: %w", core.ErrInvalidConfig)
	}
	if name == "" {
		name = DefaultRetrievalToolName
	}
	if app.cfg.Embedder == nil {
		return nil, fmt.Errorf("kit: retrieval tool %s requires an Embedder: %w", name, core.ErrInvalidConfig)
	}

	all := append([]ToolOption{
		Desc("Search the knowledge base and return the matching passages. Use it whenever the answer may be in the documents."),
	}, opts...)

	return Tool(app, name, func(ctx context.Context, in RetrievalIn) (RetrievalOut, error) {
		if _, ok := core.PrincipalFrom(ctx); !ok {
			return RetrievalOut{}, fmt.Errorf("kit: retrieval tool %s: no principal: %w", name, core.ErrUnauthorized)
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
