package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/minhngo149/RootCause/internal/analyzer"
	"github.com/minhngo149/RootCause/internal/render"
	"github.com/minhngo149/RootCause/internal/vcs"
)

func newReviewCmd() *cobra.Command {
	var scanAll bool

	cmd := &cobra.Command{
		Use:   "review [path]",
		Short: "Review SQL and Go source for query issues",
		Long: "By default, review only looks at files that are uncommitted or " +
			"committed locally but not yet pushed to the branch's upstream — " +
			"the set of things a `git push` would send plus your dirty working tree.\n" +
			"Pass --scan to walk the entire path recursively instead.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			return runReview(cmd, root, scanAll)
		},
	}

	cmd.Flags().BoolVar(&scanAll, "scan", false, "scan the entire repository instead of just changed files")
	return cmd
}

func runReview(cmd *cobra.Command, root string, scanAll bool) error {
	var relFiles []string
	var err error

	if scanAll {
		relFiles, err = walkSupportedFiles(root)
	} else {
		relFiles, err = vcs.ChangedFiles(root)
		if err != nil {
			return fmt.Errorf("%w (pass --scan to review the whole tree without git)", err)
		}
	}
	if err != nil {
		return err
	}

	d, err := newDiagnosis()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	scanned, total := 0, 0

	for _, rel := range relFiles {
		if _, ok := analyzer.ForFile(rel); !ok {
			continue
		}

		full := filepath.Join(root, rel)
		data, err := os.ReadFile(full)
		if err != nil {
			continue // e.g. listed as changed but removed since; nothing to scan
		}
		scanned++

		violations, err := d.diagnoseFile(full, data)
		if err != nil {
			return err
		}
		if len(violations) == 0 {
			continue
		}

		total += len(violations)
		fmt.Fprintf(out, "%s — %d issue(s):\n\n", full, len(violations))
		for _, v := range violations {
			render.Violation(out, v, d.docs)
		}
	}

	mode := "changed files"
	if scanAll {
		mode = "full scan"
	}
	fmt.Fprintf(out, "Scanned %d file(s) (%s), found %d issue(s) total.\n", scanned, mode, total)
	return nil
}

// walkSupportedFiles recursively collects files under root that a
// registered analyzer.Extractor exists for, skipping VCS/dependency
// directories that would otherwise drown real source in noise.
func walkSupportedFiles(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := analyzer.ForFile(p); !ok {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}
