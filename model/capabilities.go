package model

type nativeSchemaReporter interface{ SupportsNativeSchema() bool }

// SupportsNativeSchema reports whether c can enforce a JSON Schema natively; a
// nil client returns false.
func SupportsNativeSchema(c Client) bool {
	if c == nil {
		return false
	}
	if r, ok := c.(nativeSchemaReporter); ok {
		return r.SupportsNativeSchema()
	}
	return false
}

// SupportsNativeSchema reports true: the OpenAI protocol carries the schema in
// response_format.
func (c *OpenAIClient) SupportsNativeSchema() bool { return true }

// SupportsNativeSchema reports true: Ollama's format field carries the schema.
func (c *OllamaClient) SupportsNativeSchema() bool { return true }
