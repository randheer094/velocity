// Package git wraps the `git` CLI.
package git

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/randheer094/velocity/internal/config"
)

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w; stderr=%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func Clone(repoURL, dst string) error {
	_, err := run("", "clone", repoURL, dst)
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

// ConfigureAuthenticatedRemote embeds GITHUB_TOKEN in origin's URL so
// `git push` runs non-interactively.
func ConfigureAuthenticatedRemote(repoDir, repoFullName string) error {
	token, err := config.GetSecret(config.GithubTokenKey)
	if err != nil || token == "" {
		return fmt.Errorf("GITHUB_TOKEN not set in keyring")
	}
	authURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", token, repoFullName)
	_, err = run(repoDir, "remote", "set-url", "origin", authURL)
	return err
}

// CheckoutNewBranch uses `checkout -B`: creates or resets branchName.
func CheckoutNewBranch(repoDir, branchName string) error {
	if _, err := run(repoDir, "checkout", "-B", branchName); err != nil {
		return err
	}
	return nil
}

func Push(repoDir, branchName string) error {
	_, err := run(repoDir, "push", "-u", "origin", branchName)
	return err
}

// PushForceWithLease refreshes an existing PR branch on code retry;
// fails rather than clobbers if the remote has moved.
func PushForceWithLease(repoDir, branchName string) error {
	_, err := run(repoDir, "push", "--force-with-lease", "-u", "origin", branchName)
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
