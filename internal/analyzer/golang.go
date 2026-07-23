package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// queryMethods are common database call names whose first string-literal
// argument is treated as a candidate SQL query: database/sql (Query, Exec,
// Prepare, ...), sqlx (Get, Select), and gorm (Raw). This is intentionally
// a fixed name list rather than type-aware data-flow analysis — see
// docs/09-risks.md on scoping static analysis conservatively. Queries built
// via string concatenation or passed in as a variable are not detected.
var queryMethods = map[string]bool{
	"Query": true, "QueryContext": true,
	"QueryRow": true, "QueryRowContext": true,
	"Exec": true, "ExecContext": true, "MustExec": true,
	"Prepare": true, "PrepareContext": true,
	"Get": true, "Select": true,
	"Raw": true,
}

// ExtractGo walks a Go source file's AST and, for every call to a known
// database method, extracts its first string-literal argument as a
// candidate query.
func ExtractGo(filename string, src []byte) ([]Query, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var queries []Query
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !queryMethods[sel.Sel.Name] {
			return true
		}

		for _, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			queries = append(queries, Query{
				Text: text,
				Line: fset.Position(lit.Pos()).Line,
			})
			break // only the first string-literal argument per call
		}
		return true
	})
	return queries, nil
}
