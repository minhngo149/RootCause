package analyzer

import "testing"

const sample = `package db

import "context"

func listActive(db *sql.DB, id int) {
	db.Query("SELECT * FROM users WHERE id = ?", id)
}

func deleteAll(db *sql.DB, ctx context.Context) {
	db.ExecContext(ctx, ` + "`" + `DELETE FROM sessions` + "`" + `)
}

func viaVariable(db *sql.DB, query string) {
	db.Exec(query)
}

func unrelatedCall(x thing) {
	x.Select("not a database call")
}
`

func TestExtractGo(t *testing.T) {
	queries, err := ExtractGo("sample.go", []byte(sample))
	if err != nil {
		t.Fatalf("ExtractGo() error = %v", err)
	}

	// db.Query, db.ExecContext, and x.Select (matched by name only — a
	// known, documented limitation) should be found; db.Exec(query) with a
	// variable argument should not, since it has no string literal.
	if len(queries) != 3 {
		t.Fatalf("expected 3 extracted queries, got %d: %+v", len(queries), queries)
	}

	if queries[0].Text != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("queries[0].Text = %q", queries[0].Text)
	}
	if queries[0].Line != 6 {
		t.Errorf("queries[0].Line = %d, want 6", queries[0].Line)
	}

	if queries[1].Text != "DELETE FROM sessions" {
		t.Errorf("queries[1].Text = %q", queries[1].Text)
	}
}

func TestExtractGoInvalidSource(t *testing.T) {
	if _, err := ExtractGo("broken.go", []byte("not valid go (")); err == nil {
		t.Error("expected a parse error for invalid Go source, got nil")
	}
}
