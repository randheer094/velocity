// Command velocity is the single CLI binary that runs the webhook-driven
// orchestration server and exposes setup / lifecycle subcommands.
package main

import (
	"fmt"
	"os"

	"github.com/randheer094/velocity/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
