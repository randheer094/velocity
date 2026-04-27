package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/pidfile"
	"github.com/randheer094/velocity/internal/prompts"
	"github.com/randheer094/velocity/internal/resources"
)

func newUpdatePromptsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update-prompts [tag]",
		Short: "Refresh ~/.velocity/resources from the configured velocity-resources repo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Get()
			if cfg == nil {
				return errors.New("config not loaded — run `velocity setup` first")
			}
			if cfg.Resources.RepoSlug == "" {
				return errors.New("resources.repo_slug is unset — run `velocity setup` first")
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			resources.SetTimeout(cfg.Resources.FetchTimeoutSec)

			tag := ""
			if len(args) == 1 {
				tag = strings.TrimSpace(args[0])
			}
			if tag == "" {
				latest, err := resources.LatestForMajor(ctx, cfg.Resources.RepoSlug, prompts.MajorVersion)
				if err != nil {
					return fmt.Errorf("find latest tag: %w", err)
				}
				tag = latest
			}
			tag = resources.CanonicalTag(tag)
			// CheckMajor is redundant when LatestForMajor picked the
			// tag (which only returns matching majors), but it's the
			// single gate for an explicit user-supplied tag — keep
			// both branches passing through the same check.
			if err := resources.CheckMajor(tag, prompts.MajorVersion); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Downloading velocity-resources %s from %s\n", tag, cfg.Resources.RepoSlug)
			if err := resources.Install(ctx, resources.Release{RepoSlug: cfg.Resources.RepoSlug, Tag: tag}, config.ResourcesDir(), prompts.MajorVersion); err != nil {
				return err
			}

			cfg.Resources.Version = tag
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(out, "Installed resources at %s\n", config.ResourcesDir())

			entry, _ := pidfile.Read(config.PidfilePath())
			if entry.PID == 0 || !pidfile.VerifyAlive(entry) {
				// Pid recycling defence: even if the pidfile points at
				// a live pid, VerifyAlive confirms it's still our
				// velocity binary (Linux) before we send SIGHUP. A
				// stale pidfile is removed so future invocations stop
				// re-checking the same dead pid.
				_ = pidfile.Remove(config.PidfilePath())
				fmt.Fprintln(out, "daemon not running, restart to pick up changes")
				return nil
			}
			if err := syscall.Kill(entry.PID, syscall.SIGHUP); err != nil {
				if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
					fmt.Fprintln(out, "daemon not running, restart to pick up changes")
					return nil
				}
				return fmt.Errorf("signal daemon: %w", err)
			}
			fmt.Fprintf(out, "daemon SIGHUP sent (pid %d)\n", entry.PID)
			return nil
		},
	}
}
