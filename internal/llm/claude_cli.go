// Package llm shells out to the `claude` CLI.
package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

type Options struct {
	WorkingDirectory string
	Model            string
	AllowedTools     string // space-separated
	PermissionMode   string
	Timeout          time.Duration
}

// RunPrompt invokes `claude --print` with prompt as the final positional arg.
func RunPrompt(ctx context.Context, prompt string, opts Options) (string, error) {
	if prompt == "" {
		return "", errors.New("llm: empty prompt")
	}
	args := []string{
		"--print",
		"--output-format", "text",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.AllowedTools != "" {
		args = append(args, "--allowedTools", opts.AllowedTools)
	}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	args = append(args, prompt)

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	if opts.WorkingDirectory != "" {
		cmd.Dir = opts.WorkingDirectory
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	slog.Info("llm exec", "cwd", opts.WorkingDirectory, "model", opts.Model, "tools", opts.AllowedTools)
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("claude cli failed: %w; stderr=%s", err, truncate(stderr.String(), 400))
	}
	return stdout.String(), nil
}

func OptionsFromRoleConfig(role config.LLMRoleConfig, workdir string) Options {
	var timeout time.Duration
	if role.TimeoutSec > 0 {
		timeout = time.Duration(role.TimeoutSec) * time.Second
	}
	return Options{
		WorkingDirectory: workdir,
		Model:            role.Model,
		AllowedTools:     role.AllowedTools,
		PermissionMode:   role.PermissionMode,
		Timeout:          timeout,
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func DrainReader(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return strings.TrimRight(string(b), "\n")
}
