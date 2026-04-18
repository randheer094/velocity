package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
)

func newLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Print (or -f tail) the daemon log",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.LogfilePath()
			f, err := os.Open(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(os.Stderr, "no log file yet:", path)
					return nil
				}
				return err
			}
			defer f.Close()

			if _, err := io.Copy(os.Stdout, f); err != nil {
				return err
			}
			if !follow {
				return nil
			}
			for {
				time.Sleep(500 * time.Millisecond)
				if _, err := io.Copy(os.Stdout, f); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	return cmd
}
