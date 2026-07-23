package rootcause

import (
	"testing"

	"github.com/minhngo149/RootCause/internal/knowledge"
	"github.com/minhngo149/RootCause/internal/ruleengine"
)

// These tests load the real embedded rules/ and knowledge/ directories —
// not hand-written fixtures — so a YAML/front-matter mistake in an actual
// rule or article (like the colon-in-a-plain-scalar bug in SQL002.yaml)
// fails `go test ./...` instead of only surfacing when someone runs the
// built binary by hand.

func TestEmbeddedRulesLoad(t *testing.T) {
	rules, err := ruleengine.Load(RulesFS, "rules/sql")
	if err != nil {
		t.Fatalf("real rules/sql/*.yaml failed to load: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected at least one rule to load from rules/sql")
	}

	for _, r := range rules {
		if len(r.Knowledge) == 0 {
			t.Errorf("rule %s has no knowledge links", r.ID)
		}
	}
}

func TestEmbeddedKnowledgeLoads(t *testing.T) {
	docs, err := knowledge.List(KnowledgeFS, "knowledge")
	if err != nil {
		t.Fatalf("real knowledge/**/*.md failed to load: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one knowledge article to load")
	}
}

// TestRuleKnowledgeLinksResolve ensures every `knowledge:` reference in a
// rule actually points at an article that exists — a dangling link would
// silently print nothing under "Why:" instead of failing loudly.
func TestRuleKnowledgeLinksResolve(t *testing.T) {
	rules, err := ruleengine.Load(RulesFS, "rules/sql")
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	docs, err := knowledge.List(KnowledgeFS, "knowledge")
	if err != nil {
		t.Fatalf("loading knowledge: %v", err)
	}

	known := make(map[string]bool, len(docs))
	for _, d := range docs {
		known[d.ID] = true
	}

	for _, r := range rules {
		for _, k := range r.Knowledge {
			if !known[k] {
				t.Errorf("rule %s references unknown knowledge id %q", r.ID, k)
			}
		}
	}
}
