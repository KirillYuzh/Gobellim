package model

type nativeSchemaReporter interface{ SupportsNativeSchema() bool }

func SupportsNativeSchema(c Client) bool {
	if c == nil {
		return false
	}
	if r, ok := c.(nativeSchemaReporter); ok {
		return r.SupportsNativeSchema()
	}
	switch c.(type) {
	case *OpenAIClient, *OllamaClient:
		return true
	default:
		return false
	}
}

func (c *OpenAIClient) SupportsNativeSchema() bool { return true }

func (c *OllamaClient) SupportsNativeSchema() bool { return true }

func (c *GigaChatClient) SupportsNativeSchema() bool { return false }
