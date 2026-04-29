// Package git wraps the `git` CLI.
package git

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/randheer094/velocity/internal/config"
)

// run executes git with GIT_TERMINAL_PROMPT=0 so a missing or invalid
// credential never blocks waiting on stdin. Errors include the args
// (never the env), so a token injected via runAuthed cannot leak.
// stderr is redacted on the off chance git echoes it.
func run(dir string, args ...string) (string, error) {
	return runEnv(dir, nil, args...)
}

func runEnv(dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		stderrText := redactSecrets(strings.TrimSpace(stderr.String()))
		return stdout.String(), fmt.Errorf("git %s: %w; stderr=%s", strings.Join(args, " "), err, stderrText)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runAuthed runs git with GH_TOKEN injected via env-based config so
// the token never appears in cmd.Args (and therefore never in any
// error string). Falls through to plain run when GH_TOKEN is unset
// so anonymous operations against public remotes keep working.
func runAuthed(dir string, args ...string) (string, error) {
	token := os.Getenv(config.GithubTokenEnv)
	if token == "" {
		return run(dir, args...)
	}
	env := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: bearer " + token,
	}
	return runEnv(dir, env, args...)
}

// redactSecrets is a best-effort scrubber for tokens that may appear in
// git stderr (e.g. "fatal: Authentication failed for 'https://...'").
// Even though the token is never in cmd.Args, defence in depth.
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	if t := os.Getenv(config.GithubTokenEnv); t != "" {
		s = strings.ReplaceAll(s, t, "[REDACTED]")
	}
	return s
}

func Clone(repoURL, dst string) error {
	_, err := runAuthed("", "clone", repoURL, dst)
	return err
}

// DefaultBranch returns the upstream HEAD's branch (typically main).
func DefaultBranch(repoDir string) (string, error) {
	out, err := run(repoDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(out, '/'); i >= 0 {
		return out[i+1:], nil
	}
	return out, nil
}

// ConfigureAuthenticatedRemote verifies GH_TOKEN is exported so the
// daemon fails fast instead of hitting an auth error mid-push. The
// token itself is injected per-invocation via runAuthed (env-based
// git config) — it is never written into .git/config or the origin
// URL, so it cannot leak through a backed-up workspace or an error
// string.
func ConfigureAuthenticatedRemote(repoDir, repoFullName string) error {
	_ = repoDir
	_ = repoFullName
	if os.Getenv(config.GithubTokenEnv) == "" {
		return fmt.Errorf("%s env var not set", config.GithubTokenEnv)
	}
	return nil
}

// CheckoutNewBranch uses `checkout -B`: creates or resets branchName.
func CheckoutNewBranch(repoDir, branchName string) error {
	if _, err := run(repoDir, "checkout", "-B", branchName); err != nil {
		return err
	}
	return nil
}

func Push(repoDir, branchName string) error {
	_, err := runAuthed(repoDir, "push", "-u", "origin", branchName)
	return err
}

// FetchAll updates every remote ref so RebaseOnto and CheckoutBranch
// can see the latest origin state.
func FetchAll(repoDir string) error {
	_, err := runAuthed(repoDir, "fetch", "--all", "--prune")
	return err
}

// CheckoutBranch switches to an existing branch without creating one.
func CheckoutBranch(repoDir, branchName string) error {
	_, err := run(repoDir, "checkout", branchName)
	return err
}

// ResetHardToRemote points the current branch at origin/<branchName>
// so iteration picks up any commits pushed after the workspace was
// created (e.g. maintainer pushes while CI was failing).
func ResetHardToRemote(repoDir, branchName string) error {
	_, err := run(repoDir, "reset", "--hard", "origin/"+branchName)
	return err
}

// RebaseOnto rebases the current branch onto origin/<baseBranch>.
// On conflict the rebase is aborted before the error returns.
func RebaseOnto(repoDir, baseBranch string) error {
	if _, err := run(repoDir, "rebase", "origin/"+baseBranch); err != nil {
		_, _ = run(repoDir, "rebase", "--abort")
		return err
	}
	return nil
}

// WorkspaceExists is a small helper so callers don't reach into os.
func WorkspaceExists(repoDir string) bool {
	_, err := os.Stat(repoDir + "/.git")
	return err == nil
}

// PushForceWithLease refreshes an existing PR branch on code retry;
// fails rather than clobbers if the remote has moved.
func PushForceWithLease(repoDir, branchName string) error {
	_, err := runAuthed(repoDir, "push", "--force-with-lease", "-u", "origin", branchName)
	return err
}

func HasChanges(repoDir string) bool {
	out, _ := run(repoDir, "status", "--porcelain")
	return out != ""
}

// AddAllAndCommit returns false if there's nothing to commit.
func AddAllAndCommit(repoDir, message string) (bool, error) {
	if !HasChanges(repoDir) {
		return false, nil
	}
	if _, err := run(repoDir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := run(repoDir, "commit", "-m", message); err != nil {
		return false, err
	}
	slog.Info("git commit", "dir", repoDir, "msg", message)
	return true, nil
}
