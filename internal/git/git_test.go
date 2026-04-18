package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/randheer094/velocity/internal/config"
)

// TestMain isolates git's global config so commit signing / hooks from the
// test environment don't poison repository-level commands.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "git-isolate-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	gitconfig := filepath.Join(tmp, "gitconfig")
	_ = os.WriteFile(gitconfig, []byte(""), 0o644)
	_ = os.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	_ = os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	_ = os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}

func runIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func setupRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	runIn(t, dir, "init", "--bare", "--initial-branch=main", remote)

	work := filepath.Join(dir, "work")
	runIn(t, dir, "init", "--initial-branch=main", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, work, "add", ".")
	runIn(t, work, "commit", "-m", "init")
	runIn(t, work, "remote", "add", "origin", remote)
	runIn(t, work, "push", "-u", "origin", "main")
	return remote
}

func TestCloneAndDefaultBranch(t *testing.T) {
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	got, err := DefaultBranch(dst)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("default branch = %q", got)
	}
}

func TestCloneError(t *testing.T) {
	if err := Clone("/nonexistent/repo.git", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected clone error")
	}
}

func TestDefaultBranchError(t *testing.T) {
	if _, err := DefaultBranch(t.TempDir()); err == nil {
		t.Error("expected error in non-repo dir")
	}
}

func TestCheckoutNewBranch(t *testing.T) {
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatal(err)
	}
	if err := CheckoutNewBranch(dst, "PROJ-1"); err != nil {
		t.Fatalf("CheckoutNewBranch: %v", err)
	}
	out, err := exec.Command("git", "-C", dst, "rev-parse", "--abbrev-ref", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "PROJ-1" {
		t.Errorf("on branch %s", out)
	}
}

func TestCheckoutNewBranchError(t *testing.T) {
	if err := CheckoutNewBranch(t.TempDir(), "x"); err == nil {
		t.Error("expected error")
	}
}

func TestHasChangesAndCommit(t *testing.T) {
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatal(err)
	}
	if HasChanges(dst) {
		t.Error("expected no changes initially")
	}

	committed, err := AddAllAndCommit(dst, "no-op")
	if err != nil || committed {
		t.Errorf("AddAllAndCommit empty = %v, %v", committed, err)
	}

	// Configure committer for commits to work
	runIn(t, dst, "config", "user.email", "t@t")
	runIn(t, dst, "config", "user.name", "t")

	if err := os.WriteFile(filepath.Join(dst, "new.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasChanges(dst) {
		t.Error("expected changes after writing file")
	}
	committed, err = AddAllAndCommit(dst, "PROJ-1: something")
	if err != nil || !committed {
		t.Errorf("AddAllAndCommit = %v, %v", committed, err)
	}
}

func TestPushAndForceWithLease(t *testing.T) {
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatal(err)
	}
	runIn(t, dst, "config", "user.email", "t@t")
	runIn(t, dst, "config", "user.name", "t")
	if err := CheckoutNewBranch(dst, "feat"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "f.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddAllAndCommit(dst, "x"); err != nil {
		t.Fatal(err)
	}
	if err := Push(dst, "feat"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "f.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AddAllAndCommit(dst, "y"); err != nil {
		t.Fatal(err)
	}
	if err := PushForceWithLease(dst, "feat"); err != nil {
		t.Fatalf("PushForceWithLease: %v", err)
	}
}

func TestPushError(t *testing.T) {
	if err := Push(t.TempDir(), "x"); err == nil {
		t.Error("expected push error")
	}
	if err := PushForceWithLease(t.TempDir(), "x"); err == nil {
		t.Error("expected push error")
	}
}

func TestConfigureAuthenticatedRemoteMissingToken(t *testing.T) {
	keyring.MockInit()
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureAuthenticatedRemote(dst, "owner/repo"); err == nil {
		t.Error("expected error when token unset")
	}
}

func TestConfigureAuthenticatedRemoteWithToken(t *testing.T) {
	keyring.MockInit()
	if err := config.SetSecret(config.GithubTokenKey, "abc"); err != nil {
		t.Fatal(err)
	}
	remote := setupRemote(t)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(remote, dst); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureAuthenticatedRemote(dst, "owner/repo"); err != nil {
		t.Errorf("ConfigureAuthenticatedRemote: %v", err)
	}
}
