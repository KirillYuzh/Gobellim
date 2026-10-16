package gliner

func keepWord(emitted, nTokens, limit int, kept bool) bool {
	return !kept || emitted+nTokens <= limit
}
