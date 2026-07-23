package analyzer

// ExtractSQL treats the entire file as one block of query text starting at
// line 1. This is the extractor for .sql files, and the fallback for any
// file extension without a language-specific extractor (query logs,
// EXPLAIN dumps, plain text).
func ExtractSQL(_ string, src []byte) ([]Query, error) {
	return []Query{{Text: string(src), Line: 1}}, nil
}
