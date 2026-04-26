package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the velocity binary version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "velocity %s (manifest major %d)\n", version.String(), version.Major)
			return nil
		},
	}
}
