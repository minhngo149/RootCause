package ruleengine

import "regexp"

type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// Detect describes how a Rule looks for a violation. Type selects which
// fields below are relevant; unknown types are rejected at load time.
type Detect struct {
	Type                string   `yaml:"type"`
	Pattern             string   `yaml:"pattern,omitempty"`
	StatementStartsWith []string `yaml:"statement_starts_with,omitempty"`
	RequiresKeyword     string   `yaml:"requires_keyword,omitempty"`
}

// Rule never contains knowledge itself — it only references Knowledge docs
// by ID and carries just enough to detect and recommend.
type Rule struct {
	ID             string   `yaml:"id"`
	Name           string   `yaml:"name"`
	Severity       Severity `yaml:"severity"`
	Detect         Detect   `yaml:"detect"`
	Knowledge      []string `yaml:"knowledge"`
	Recommendation []string `yaml:"recommendation"`

	pattern *regexp.Regexp
}

// Violation is one match of a Rule against a piece of source text.
type Violation struct {
	Rule    *Rule
	Line    int
	Excerpt string
}
