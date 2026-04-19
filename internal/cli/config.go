package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
)

func newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the current config.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.ConfigPath()
			data, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("config.yaml not found at %s — copy config.example.yaml and edit", path)
				}
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}
