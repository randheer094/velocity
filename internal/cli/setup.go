package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/prompts"
	"github.com/randheer094/velocity/internal/resources"
)

var repoSlugRE = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

func newSetupCmd() *cobra.Command {
	var repoFlag, versionFlag string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Download the velocity-resources release tarball into ~/.velocity/resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Get()
			if cfg == nil {
				if e := config.LoadError(); e != "" {
					return fmt.Errorf("%s\nFix %s (see config.example.yaml)", e, config.ConfigPath())
				}
				return fmt.Errorf("velocity is not configured: write %s (see config.example.yaml)", config.ConfigPath())
			}

			out := cmd.OutOrStdout()
			// Build one bufio.Reader for the whole interactive flow —
			// constructing a fresh wrapper per prompt would discard any
			// bytes the previous wrapper had buffered, which breaks
			// piped multi-line input (e.g. `printf "x\ny\n" | velocity setup`).
			in := bufio.NewReader(cmd.InOrStdin())

			repoSlug := strings.TrimSpace(repoFlag)
			if repoSlug == "" {
				prompted, err := promptString(in, out, "Resources repo (<owner>/<repo>)", cfg.Resources.RepoSlug)
				if err != nil {
					return err
				}
				repoSlug = prompted
			}
			repoSlug, err := normalizeRepoSlug(repoSlug)
			if err != nil {
				return err
			}

			tag := strings.TrimSpace(versionFlag)
			if tag == "" {
				prompted, err := promptString(in, out, "Version (release tag, e.g. v0.6.0)", cfg.Resources.Version)
				if err != nil {
					return err
				}
				tag = prompted
			}
			if tag == "" {
				return fmt.Errorf("version is required")
			}
			if err := resources.CheckMajor(tag, prompts.MajorVersion); err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			resources.SetTimeout(cfg.Resources.FetchTimeoutSec)
			fmt.Fprintf(out, "Downloading velocity-resources %s from %s\n", tag, repoSlug)
			if err := resources.Install(ctx, resources.Release{RepoSlug: repoSlug, Tag: tag}, config.ResourcesDir(), prompts.MajorVersion); err != nil {
				return err
			}

			cfg.Resources.RepoSlug = repoSlug
			cfg.Resources.Version = tag
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			fmt.Fprintf(out, "Installed resources at %s\n", config.ResourcesDir())
			fmt.Fprintf(out, "Pinned %s@%s in %s\n", repoSlug, tag, config.ConfigPath())
			return nil
		},
	}
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Resources repo slug, e.g. randheer094/velocity-resources")
	cmd.Flags().StringVar(&versionFlag, "version", "", "Release tag, e.g. v0.6.0")
	return cmd
}

// normalizeRepoSlug strips http(s):// + github.com/ prefixes (with a
// warning) and validates the bare <owner>/<repo> shape.
func normalizeRepoSlug(s string) (string, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return "", fmt.Errorf("resources repo slug is required")
	}
	t = strings.TrimPrefix(t, "https://")
	t = strings.TrimPrefix(t, "http://")
	t = strings.TrimPrefix(t, "github.com/")
	t = strings.TrimSuffix(t, ".git")
	t = strings.TrimSuffix(t, "/")
	if !repoSlugRE.MatchString(t) {
		return "", fmt.Errorf("invalid repo slug %q: expected <owner>/<repo>", s)
	}
	return t, nil
}

// promptString writes label (with default if non-empty) and reads a
// trimmed line from in. Empty input returns the default. The caller
// must reuse the same *bufio.Reader across prompts so no buffered
// bytes are dropped between calls.
func promptString(in *bufio.Reader, out io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	// Warn the caller when the input looks URL-shaped. The actual
	// stripping happens in normalizeRepoSlug; this only nudges the
	// user before we silently accept their copy-paste.
	if strings.Contains(line, "github.com/") || strings.HasPrefix(line, "http") {
		fmt.Fprintln(out, "  (note: stripping URL prefix — repo_slug is stored as <owner>/<repo>)")
	}
	return line, nil
}
