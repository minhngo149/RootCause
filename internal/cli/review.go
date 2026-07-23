package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/minhngo149/RootCause/internal/render"
)

func newReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "review <path>",
		Short: "Recursively review every .sql file under a directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd, args[0])
		},
	}
}

func runReview(cmd *cobra.Command, root string) error {
	out := cmd.OutOrStdout()
	scanned, total := 0, 0

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".sql" {
			return nil
		}
		scanned++

		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", p, err)
		}

		violations, docs, err := diagnose(string(data))
		if err != nil {
			return err
		}
		if len(violations) == 0 {
			return nil
		}

		total += len(violations)
		fmt.Fprintf(out, "%s — %d issue(s):\n\n", p, len(violations))
		for _, v := range violations {
			render.Violation(out, v, docs)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking %s: %w", root, err)
	}

	fmt.Fprintf(out, "Scanned %d .sql file(s), found %d issue(s) total.\n", scanned, total)
	return nil
}
