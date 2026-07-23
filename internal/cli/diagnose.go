package cli

import (
	"fmt"

	rootcause "github.com/minhngo149/RootCause"
	"github.com/minhngo149/RootCause/internal/analyzer"
	"github.com/minhngo149/RootCause/internal/knowledge"
	"github.com/minhngo149/RootCause/internal/ruleengine"
)

// diagnosis is the shared pipeline behind `doctor` and `review`:
// source file -> language Extractor -> Rule Engine -> Knowledge lookup.
type diagnosis struct {
	engine *ruleengine.Engine
	docs   map[string]*knowledge.Doc
}

func newDiagnosis() (*diagnosis, error) {
	rules, err := ruleengine.Load(rootcause.RulesFS, "rules")
	if err != nil {
		return nil, fmt.Errorf("loading rules: %w", err)
	}

	docs, err := knowledge.List(rootcause.KnowledgeFS, "knowledge")
	if err != nil {
		return nil, fmt.Errorf("loading knowledge: %w", err)
	}
	docByID := make(map[string]*knowledge.Doc, len(docs))
	for i := range docs {
		docByID[docs[i].ID] = &docs[i]
	}

	return &diagnosis{engine: ruleengine.NewEngine(rules), docs: docByID}, nil
}

// diagnoseFile extracts candidate queries from a source file — using a
// language-specific extractor when one is registered for its extension,
// otherwise treating the whole file as SQL/text — and runs each one
// through the Rule Engine, translating violation line numbers back to
// filename's own line numbers.
func (d *diagnosis) diagnoseFile(filename string, src []byte) ([]ruleengine.Violation, error) {
	extract := analyzer.ForFileOrDefault(filename)
	queries, err := extract(filename, src)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filename, err)
	}

	var violations []ruleengine.Violation
	for _, q := range queries {
		for _, v := range d.engine.Analyze(q.Text) {
			v.Line = q.Line + v.Line - 1
			violations = append(violations, v)
		}
	}
	return violations, nil
}
