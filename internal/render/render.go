// Package render turns rule violations and knowledge articles into
// terminal-friendly output. This is where the "Explain WHY" philosophy
// becomes visible to the user, so it gets more care than a plain printf.
package render

import (
	"fmt"
	"io"

	"github.com/charmbracelet/glamour"

	"github.com/minhngo149/RootCause/internal/knowledge"
	"github.com/minhngo149/RootCause/internal/ruleengine"
)

func severityBadge(s ruleengine.Severity) string {
	switch s {
	case ruleengine.SeverityHigh:
		return "\x1b[1;41;97m HIGH \x1b[0m"
	case ruleengine.SeverityMedium:
		return "\x1b[1;43;30m MEDIUM \x1b[0m"
	default:
		return "\x1b[1;46;30m LOW \x1b[0m"
	}
}

// Violation prints one rule violation, its recommendation, and the
// knowledge articles it links to.
func Violation(w io.Writer, v ruleengine.Violation, docs map[string]*knowledge.Doc) {
	fmt.Fprintf(w, "%s %s (%s)\n", severityBadge(v.Rule.Severity), v.Rule.Name, v.Rule.ID)
	fmt.Fprintf(w, "  line %d: %s\n", v.Line, v.Excerpt)

	if len(v.Rule.Recommendation) > 0 {
		fmt.Fprintln(w, "  Recommendation:")
		for _, r := range v.Rule.Recommendation {
			fmt.Fprintf(w, "    - %s\n", r)
		}
	}

	for _, k := range v.Rule.Knowledge {
		if d, ok := docs[k]; ok {
			fmt.Fprintf(w, "  Why: %s -> rootcause explain %s\n", d.Title, d.ID)
		}
	}
	fmt.Fprintln(w)
}

// Markdown renders a knowledge article body for the terminal, falling back
// to raw text if the terminal renderer can't be constructed.
func Markdown(w io.Writer, body string) error {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		_, ferr := fmt.Fprintln(w, body)
		return ferr
	}

	out, err := r.Render(body)
	if err != nil {
		_, ferr := fmt.Fprintln(w, body)
		return ferr
	}
	_, err = fmt.Fprint(w, out)
	return err
}
