package ruleengine

import (
	"regexp"
	"sort"
	"strings"
)

// Engine runs a fixed set of Rules against source text. It is deterministic
// and has no dependency on AI — the same input always yields the same
// violations.
type Engine struct {
	rules []*Rule
}

func NewEngine(rules []*Rule) *Engine {
	return &Engine{rules: rules}
}

func (e *Engine) Rules() []*Rule {
	return e.rules
}

// Analyze runs every loaded rule against source and returns all violations,
// ordered by line number.
func (e *Engine) Analyze(source string) []Violation {
	var violations []Violation

	for _, rule := range e.rules {
		switch rule.Detect.Type {
		case "regex":
			violations = append(violations, detectRegex(rule, source)...)
		case "statement_missing_keyword":
			violations = append(violations, detectMissingKeyword(rule, source)...)
		}
	}

	sort.SliceStable(violations, func(i, j int) bool {
		return violations[i].Line < violations[j].Line
	})
	return violations
}

func detectRegex(rule *Rule, source string) []Violation {
	var out []Violation
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		if rule.pattern.MatchString(line) {
			out = append(out, Violation{
				Rule:    rule,
				Line:    i + 1,
				Excerpt: strings.TrimSpace(line),
			})
		}
	}
	return out
}

var firstWordPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func detectMissingKeyword(rule *Rule, source string) []Violation {
	starts := make(map[string]bool, len(rule.Detect.StatementStartsWith))
	for _, s := range rule.Detect.StatementStartsWith {
		starts[strings.ToLower(s)] = true
	}
	requiresRe := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(rule.Detect.RequiresKeyword) + `\b`)

	var out []Violation
	pos := 0
	for _, stmt := range strings.Split(source, ";") {
		stmtStart := pos
		pos += len(stmt) + 1 // account for the ';' consumed by Split

		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}

		firstWord := strings.ToLower(firstWordPattern.FindString(trimmed))
		if !starts[firstWord] {
			continue
		}
		if requiresRe.MatchString(trimmed) {
			continue
		}

		leading := len(stmt) - len(strings.TrimLeft(stmt, " \t\r\n"))
		line := strings.Count(source[:stmtStart+leading], "\n") + 1

		excerpt := trimmed
		if idx := strings.IndexByte(excerpt, '\n'); idx >= 0 {
			excerpt = excerpt[:idx] + " ..."
		}

		out = append(out, Violation{Rule: rule, Line: line, Excerpt: excerpt})
	}
	return out
}
