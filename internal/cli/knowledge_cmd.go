package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	rootcause "github.com/minhngo149/RootCause"
	"github.com/minhngo149/RootCause/internal/knowledge"
	"github.com/minhngo149/RootCause/internal/render"
)

// explain and learn both surface the Knowledge base for now; `explain` is
// meant for "why did the tool flag this" lookups and `learn` for browsing
// topics on your own, but v1 shares the same rendering path for both.

func newExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain [topic]",
		Short: "Explain a concept referenced by a rule (e.g. a violation you just saw)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKnowledge(cmd, args)
		},
	}
}

func newLearnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "learn [topic]",
		Short: "Browse the knowledge base by topic",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKnowledge(cmd, args)
		},
	}
}

func runKnowledge(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	docs, err := knowledge.List(rootcause.KnowledgeFS, "knowledge")
	if err != nil {
		return fmt.Errorf("loading knowledge: %w", err)
	}

	if len(args) == 0 {
		fmt.Fprintln(out, "Available topics:")
		for _, d := range docs {
			fmt.Fprintf(out, "  - %s\n", d.ID)
		}
		return nil
	}

	topic := args[0]
	for i := range docs {
		if docs[i].ID == topic {
			return render.Markdown(out, docs[i].Body)
		}
	}

	fmt.Fprintf(out, "No knowledge article found for %q.\n\nAvailable topics:\n", topic)
	for _, d := range docs {
		fmt.Fprintf(out, "  - %s\n", d.ID)
	}
	return nil
}
