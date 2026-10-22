package model

// ContextTokens returns the configured context window in tokens.
func (c *OpenAIClient) ContextTokens() int { return c.cfg.ContextTokens }

// ContextTokens returns the configured context window in tokens.
func (c *OllamaClient) ContextTokens() int { return c.cfg.ContextTokens }
