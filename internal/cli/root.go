// Package cli builds the cobra command tree: setup, start, stop, restart, status, logs.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
)

const defaultDir = "~/.velocity"

func NewRootCmd() *cobra.Command {
	var dir string
	root := &cobra.Command{
		Use:           "velocity",
		Short:         "Webhook-driven Jira → PR agent (arch + code)",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			config.SetDir(dir)
		},
	}
	root.PersistentFlags().StringVar(&dir, "dir", defaultDir, "velocity data directory")

	root.AddCommand(
		newSetupCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newLogsCmd(),
	)
	return root
}
