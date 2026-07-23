// Package cli wires the four RootCause commands (doctor, review, explain,
// learn) on top of cobra. The CLI is just a client — it holds no detection
// or knowledge logic of its own, only orchestration and rendering.
package cli

import "github.com/spf13/cobra"

const version = "0.1.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "rootcause",
		Short:   "Find the root cause, not the symptoms.",
		Long:    "RootCause is a production diagnosis CLI.\nIt runs deterministic rules against your SQL, then explains WHY an issue matters using a curated knowledge base.",
		Version: version,

		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(newDoctorCmd())
	root.AddCommand(newReviewCmd())
	root.AddCommand(newExplainCmd())
	root.AddCommand(newLearnCmd())

	return root
}

func Execute() error {
	return NewRootCmd().Execute()
}
