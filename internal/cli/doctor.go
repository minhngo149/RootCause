package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/minhngo149/RootCause/internal/render"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor <file>",
		Short: "Diagnose a single file: SQL, a query log/EXPLAIN dump, or a Go source file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, args[0])
		},
	}
}

func runDoctor(cmd *cobra.Command, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", file, err)
	}

	d, err := newDiagnosis()
	if err != nil {
		return err
	}

	violations, err := d.diagnoseFile(file, data)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(violations) == 0 {
		fmt.Fprintf(out, "%s: no issues found by the current rules.\n", file)
		return nil
	}

	fmt.Fprintf(out, "%s — %d issue(s) found:\n\n", file, len(violations))
	for _, v := range violations {
		render.Violation(out, v, d.docs)
	}
	return nil
}
