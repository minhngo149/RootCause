package ruleengine

import (
	"testing"
	"testing/fstest"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()

	fsys := fstest.MapFS{
		"sql/SQL001.yaml": &fstest.MapFile{Data: []byte(`
id: SQL001
name: Avoid SELECT *
severity: medium
detect:
  type: regex
  pattern: '(?i)select\s+\*\s+from'
knowledge: [execution-plan, covering-index]
recommendation: ["Specify only required columns."]
`)},
		"sql/SQL002.yaml": &fstest.MapFile{Data: []byte(`
id: SQL002
name: UPDATE or DELETE without WHERE clause
severity: high
detect:
  type: statement_missing_keyword
  statement_starts_with: [update, delete]
  requires_keyword: where
knowledge: [missing-where-clause]
recommendation: ["Add an explicit WHERE clause."]
`)},
	}

	rules, err := Load(fsys, "sql")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("Load() returned %d rules, want 2", len(rules))
	}
	return NewEngine(rules)
}

func hasRule(violations []Violation, id string) bool {
	for _, v := range violations {
		if v.Rule.ID == id {
			return true
		}
	}
	return false
}

func TestSQL001SelectStar(t *testing.T) {
	e := testEngine(t)

	got := e.Analyze("SELECT * FROM users WHERE id = 1;")
	if !hasRule(got, "SQL001") {
		t.Errorf("expected SQL001 violation, got %+v", got)
	}

	got = e.Analyze("SELECT id, name FROM users WHERE id = 1;")
	if hasRule(got, "SQL001") {
		t.Errorf("did not expect SQL001 violation for explicit columns, got %+v", got)
	}
}

func TestSQL002MissingWhere(t *testing.T) {
	e := testEngine(t)

	got := e.Analyze("DELETE FROM users;")
	if !hasRule(got, "SQL002") {
		t.Errorf("expected SQL002 violation for DELETE without WHERE, got %+v", got)
	}

	got = e.Analyze("UPDATE users SET active = false;")
	if !hasRule(got, "SQL002") {
		t.Errorf("expected SQL002 violation for UPDATE without WHERE, got %+v", got)
	}

	got = e.Analyze("DELETE FROM users WHERE id = 1;")
	if hasRule(got, "SQL002") {
		t.Errorf("did not expect SQL002 violation when WHERE is present, got %+v", got)
	}

	got = e.Analyze("SELECT * FROM users;")
	if hasRule(got, "SQL002") {
		t.Errorf("SQL002 must not fire on SELECT statements, got %+v", got)
	}
}

func TestMultiStatementLineNumbers(t *testing.T) {
	e := testEngine(t)

	source := "SELECT id FROM users WHERE id = 1;\nDELETE FROM sessions;\n"
	got := e.Analyze(source)
	if !hasRule(got, "SQL002") {
		t.Fatalf("expected SQL002 violation, got %+v", got)
	}
	for _, v := range got {
		if v.Rule.ID == "SQL002" && v.Line != 2 {
			t.Errorf("expected SQL002 violation on line 2, got line %d", v.Line)
		}
	}
}
