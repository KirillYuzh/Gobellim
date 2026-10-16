package model

func (c *OpenAIClient) ContextTokens() int { return c.cfg.ContextTokens }

func (c *OllamaClient) ContextTokens() int { return c.cfg.ContextTokens }

func (c *GigaChatClient) ContextTokens() int { return c.cfg.ContextTokens }
