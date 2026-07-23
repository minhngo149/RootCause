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
	rules, err := ruleengine.Load(RulesFS, "rules")
	if err != nil {
		t.Fatalf("real rules/**/*.yaml failed to load: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected at least one rule to load from rules/")
	}

	for _, r := range rules {
		if len(r.Knowledge) == 0 {
			t.Errorf("rule %s has no knowledge links", r.ID)
		}
	}
}

// TestEmbeddedMongoRules exercises the real MONGO001/MONGO002 rules against
// the exact query text internal/analyzer.ExtractGo would produce, so a
// change to either the rule pattern or the extractor's output format that
// breaks the pairing is caught here instead of only at `rootcause doctor`
// runtime.
func TestEmbeddedMongoRules(t *testing.T) {
	rules, err := ruleengine.Load(RulesFS, "rules")
	if err != nil {
		t.Fatalf("loading rules: %v", err)
	}
	engine := ruleengine.NewEngine(rules)

	hasRule := func(text, id string) bool {
		for _, v := range engine.Analyze(text) {
			if v.Rule.ID == id {
				return true
			}
		}
		return false
	}

	if !hasRule("DeleteMany(bson.M{})", "MONGO001") {
		t.Error("expected MONGO001 for an empty-filter DeleteMany")
	}
	if !hasRule("UpdateMany(bson.D{})", "MONGO001") {
		t.Error("expected MONGO001 for an empty-filter UpdateMany")
	}
	if hasRule(`DeleteMany(bson.M{"expired_at": bson.M{"$lt": cutoff}})`, "MONGO001") {
		t.Error("did not expect MONGO001 when a real filter is present")
	}
	if hasRule(`Find(bson.M{})`, "MONGO001") {
		t.Error("MONGO001 must only fire on DeleteMany/UpdateMany, not Find")
	}

	if !hasRule(`Find(bson.M{"$where": "this.a == this.b"})`, "MONGO002") {
		t.Error("expected MONGO002 when $where is used")
	}
	if hasRule(`Find(bson.M{"status": "active"})`, "MONGO002") {
		t.Error("did not expect MONGO002 for a plain filter")
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
	rules, err := ruleengine.Load(RulesFS, "rules")
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
