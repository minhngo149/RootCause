package cli

import (
	"fmt"

	rootcause "github.com/minhngo149/RootCause"
	"github.com/minhngo149/RootCause/internal/knowledge"
	"github.com/minhngo149/RootCause/internal/ruleengine"
)

// diagnose is the shared pipeline behind `doctor` and `review`:
// Input -> Rule Engine -> Knowledge lookup -> Violations + Docs.
func diagnose(source string) ([]ruleengine.Violation, map[string]*knowledge.Doc, error) {
	rules, err := ruleengine.Load(rootcause.RulesFS, "rules/sql")
	if err != nil {
		return nil, nil, fmt.Errorf("loading rules: %w", err)
	}

	docs, err := knowledge.List(rootcause.KnowledgeFS, "knowledge")
	if err != nil {
		return nil, nil, fmt.Errorf("loading knowledge: %w", err)
	}
	docByID := make(map[string]*knowledge.Doc, len(docs))
	for i := range docs {
		docByID[docs[i].ID] = &docs[i]
	}

	engine := ruleengine.NewEngine(rules)
	violations := engine.Analyze(source)
	return violations, docByID, nil
}
