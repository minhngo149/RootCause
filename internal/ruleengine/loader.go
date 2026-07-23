package ruleengine

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// Load reads every *.yaml/*.yml file under dir in fsys and parses it into a
// Rule. Community contributors only ever need to add a file here — the
// engine itself does not change.
func Load(fsys fs.FS, dir string) ([]*Rule, error) {
	var rules []*Rule

	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := path.Ext(p)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		var r Rule
		if err := yaml.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if err := r.validate(); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		rules = append(rules, &r)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	return rules, nil
}

func (r *Rule) validate() error {
	if r.ID == "" {
		return errors.New("missing id")
	}
	if r.Name == "" {
		return errors.New("missing name")
	}
	switch r.Severity {
	case SeverityLow, SeverityMedium, SeverityHigh:
	default:
		return fmt.Errorf("rule %s: unknown severity %q", r.ID, r.Severity)
	}

	switch r.Detect.Type {
	case "regex":
		if r.Detect.Pattern == "" {
			return fmt.Errorf("rule %s: regex detector requires pattern", r.ID)
		}
		re, err := regexp.Compile(r.Detect.Pattern)
		if err != nil {
			return fmt.Errorf("rule %s: invalid pattern: %w", r.ID, err)
		}
		r.pattern = re
	case "statement_missing_keyword":
		if len(r.Detect.StatementStartsWith) == 0 || r.Detect.RequiresKeyword == "" {
			return fmt.Errorf("rule %s: statement_missing_keyword detector requires statement_starts_with and requires_keyword", r.ID)
		}
	case "":
		return fmt.Errorf("rule %s: missing detect.type", r.ID)
	default:
		return fmt.Errorf("rule %s: unknown detect.type %q", r.ID, r.Detect.Type)
	}
	return nil
}
