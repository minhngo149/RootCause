package analyzer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
)

// sqlMethods are common database call names whose first string-literal
// argument is treated as a candidate SQL query: database/sql (Query, Exec,
// Prepare, ...), sqlx (Get, Select), and gorm (Raw). This is intentionally
// a fixed name list rather than type-aware data-flow analysis — see
// docs/09-risks.md on scoping static analysis conservatively. Queries built
// via string concatenation or passed in as a variable are not detected.
var sqlMethods = map[string]bool{
	"Query": true, "QueryContext": true,
	"QueryRow": true, "QueryRowContext": true,
	"Exec": true, "ExecContext": true, "MustExec": true,
	"Prepare": true, "PrepareContext": true,
	"Get": true, "Select": true,
	"Raw": true,
}

// mongoMethods are *mongo.Collection methods (MongoDB's official Go
// driver) whose filter/update/document argument is treated as a candidate
// query. Unlike SQL, this argument is a Go composite literal (bson.M{...},
// bson.D{...}), not a string, so it's rendered back to source text with
// go/printer instead of unquoted — that printed text is what MongoDB rules
// pattern-match against. This assumes the idiomatic driver signature
// `Method(ctx, filter, ...)`; calls that don't follow it are skipped.
var mongoMethods = map[string]bool{
	"Find": true, "FindOne": true,
	"FindOneAndDelete": true, "FindOneAndUpdate": true, "FindOneAndReplace": true,
	"InsertOne": true, "InsertMany": true,
	"UpdateOne": true, "UpdateMany": true,
	"DeleteOne": true, "DeleteMany": true,
	"ReplaceOne": true,
	"Aggregate": true, "CountDocuments": true,
}

// ExtractGo walks a Go source file's AST and, for every call to a known
// SQL or MongoDB method, extracts a candidate query.
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
		if !ok {
			return true
		}

		switch {
		case sqlMethods[sel.Sel.Name]:
			if text, ok := firstStringLiteralArg(call); ok {
				queries = append(queries, Query{
					Text: text,
					Line: fset.Position(call.Pos()).Line,
				})
			}
		case mongoMethods[sel.Sel.Name]:
			if q, ok := mongoQueryText(fset, call, sel.Sel.Name); ok {
				queries = append(queries, q)
			}
		}
		return true
	})
	return queries, nil
}

func firstStringLiteralArg(call *ast.CallExpr) (string, bool) {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if text, err := strconv.Unquote(lit.Value); err == nil {
			return text, true
		}
	}
	return "", false
}

// mongoQueryText renders "<Method>(<filterOrUpdateArg>)", e.g.
// "DeleteMany(bson.M{})", so a rule can pattern-match on both the
// operation and the shape of its argument.
func mongoQueryText(fset *token.FileSet, call *ast.CallExpr, method string) (Query, bool) {
	if len(call.Args) < 2 {
		return Query{}, false
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, call.Args[1]); err != nil {
		return Query{}, false
	}
	return Query{
		Text: fmt.Sprintf("%s(%s)", method, buf.String()),
		Line: fset.Position(call.Pos()).Line,
	}, true
}
