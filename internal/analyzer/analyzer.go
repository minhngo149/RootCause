// Package analyzer extracts candidate SQL query text out of a source file,
// regardless of what language that file is written in. The Rule Engine
// itself stays language-agnostic — it only ever sees extracted query text.
package analyzer

import "path/filepath"

// Query is one SQL statement (or SQL-like text) found in a source file,
// together with the 1-based line in that file where it starts.
type Query struct {
	Text string
	Line int
}

// Extractor pulls candidate queries out of one source file's content.
type Extractor func(filename string, src []byte) ([]Query, error)

// extractors maps a file extension to its language-specific extractor.
// Add an entry here to support a new language — nothing else in the
// pipeline needs to change.
var extractors = map[string]Extractor{
	".go":  ExtractGo,
	".sql": ExtractSQL,
}

// ForFile returns the extractor registered for filename's extension, and
// whether one was found. `review` uses this to decide which files in a
// directory or changed-file list are even worth reading.
func ForFile(filename string) (Extractor, bool) {
	e, ok := extractors[filepath.Ext(filename)]
	return e, ok
}

// ForFileOrDefault is like ForFile but falls back to treating the whole
// file as raw SQL/text when the extension isn't recognized. `doctor`
// operates on exactly the file the user pointed at (which may be a
// slow-query log or EXPLAIN dump with no meaningful extension), so it
// always needs *some* extractor rather than "unsupported".
func ForFileOrDefault(filename string) Extractor {
	if e, ok := ForFile(filename); ok {
		return e
	}
	return ExtractSQL
}
