package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

const structureInstruction = "Convert the following text into JSON that validates against the provided JSON Schema. Return only JSON."

// Structurer turns prose into JSON matching Schema using one chat call; an
// empty Instructions field uses a built-in instruction.
type Structurer struct {
	Client       Client
	Schema       json.RawMessage
	SchemaMode   SchemaMode
	MaxTokens    int
	Temperature  *float64
	Instructions string
}

func (s Structurer) instruction() string {
	if s.Instructions != "" {
		return s.Instructions
	}
	return structureInstruction
}

// Structure asks Client for JSON matching Schema and returns the raw text
// without validating it. It returns an error wrapping core.ErrInvalidConfig
// when Client or Schema is missing, and downgrades SchemaNative to SchemaInline
// for clients without native schema support.
func (s Structurer) Structure(ctx context.Context, prose string) (string, error) {
	if s.Client == nil {
		return "", fmt.Errorf("model: structurer: Client is required: %w", core.ErrInvalidConfig)
	}
	if len(s.Schema) == 0 {
		return "", fmt.Errorf("model: structurer: Schema is required: %w", core.ErrInvalidConfig)
	}

	mode := s.SchemaMode
	if mode == SchemaNative && !SupportsNativeSchema(s.Client) {
		mode = SchemaInline
	}

	msgs, native := SchemaPlan(Request{
		Messages:   []Message{{Role: RoleUser, Content: s.instruction()}},
		JSONSchema: s.Schema,
		SchemaMode: mode,
	})
	msgs[len(msgs)-1].Content += "\n\n" + fenceProse(prose)

	req := Request{
		Messages:    msgs,
		MaxTokens:   s.MaxTokens,
		Temperature: s.Temperature,
		SchemaMode:  mode,
	}
	if native {
		req.JSONSchema = s.Schema
	}

	resp, err := s.Client.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

func fenceProse(prose string) string {
	fence := "```"
	for strings.Contains(prose, fence) {
		fence += "`"
	}
	return fence + "\n" + prose + "\n" + fence
}
