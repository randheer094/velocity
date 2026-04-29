package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

func TestNewConfigCmdMissingFile(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	cmd := newConfigCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml not found") {
		t.Errorf("expected not-found hint, got %v", err)
	}
}

func TestNewConfigCmdReadsFile(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	body := "jira:\n  base_url: https://x\n"
	if err := os.WriteFile(config.ConfigPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newConfigCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "base_url") {
		t.Errorf("output = %q", out.String())
	}
}

// TestNewConfigCmdReadFails covers the generic-error branch (path is a directory).
func TestNewConfigCmdReadFails(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := os.MkdirAll(config.ConfigPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := newConfigCmd()
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Error("expected non-NotExist error")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected NotFound: %v", err)
	}
}

// TestRootPersistentPreRun ensures invoking the root cmd executes the pre-run
// hook that calls config.SetDir.
func TestRootPersistentPreRun(t *testing.T) {
	dir := t.TempDir()
	root := NewRootCmd()
	root.SetArgs([]string{"--dir", dir, "config"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	_ = root.Execute()
	if config.AgentDir != dir {
		t.Errorf("AgentDir = %q, want %q", config.AgentDir, dir)
	}
}

// TestReadPidNonNotExistError covers the err != ErrNotExist branch.
func TestReadPidNonNotExistError(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	// Create the pidfile path as a directory so ReadFile returns "is a directory".
	if err := os.MkdirAll(config.PidfilePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readPid(); err == nil {
		t.Error("expected error from directory-as-pidfile")
	}
}

// TestWritePidEnsureDirError covers the EnsureDir-returns-error branch.
func TestWritePidEnsureDirError(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set AgentDir under a regular file → MkdirAll fails with ENOTDIR.
	config.SetDir(filepath.Join(blocker, "nested"))
	defer config.SetDir(t.TempDir())
	if err := writePid(123); err == nil {
		t.Error("expected EnsureDir error")
	}
}

// TestDetachEnsureDirError covers the matching branch in detach().
func TestDetachEnsureDirError(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(filepath.Join(blocker, "nested"))
	defer config.SetDir(t.TempDir())
	if err := detach(); err == nil {
		t.Error("expected EnsureDir error from detach")
	}
}

// TestDetachLogfileError covers the OpenFile-error branch in detach().
func TestDetachLogfileError(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	// Make the logfile path a directory → OpenFile O_WRONLY fails.
	if err := os.MkdirAll(config.LogfilePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := detach(); err == nil {
		t.Error("expected OpenFile error from detach")
	}
}

// TestNewLogsCmdPermissionError covers the non-NotExist Open error branch.
func TestNewLogsCmdPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses permission denial")
	}
	parent := t.TempDir()
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "daemon.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Strip read+exec from sub so opening the file inside fails with EACCES.
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o755)
	config.SetDir(sub)
	cmd := newLogsCmd()
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected permission error")
	}
}

// TestNewRestartCmdSkipsStalePidfile verifies restart treats a
// pidfile pointing at a dead pid as "not running" rather than erroring
// out — pidfile.VerifyAlive filters the stale entry, the file is
// removed, and detach() proceeds to spawn a fresh daemon.
func TestNewRestartCmdSkipsStalePidfile(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	writeValidConfig(t)
	// Stale pid that almost certainly doesn't exist.
	if err := writePid(2147483646); err != nil {
		t.Fatal(err)
	}
	cmd := newRestartCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Errorf("restart with stale pidfile should succeed, got %v", err)
	}
	// detach() wrote a fresh pidfile.
	if pid, _ := readPid(); pid <= 0 || pid == 2147483646 {
		t.Errorf("expected fresh pidfile, got %d", pid)
	}
}

// TestNewStartCmdForegroundReachesServerRun covers the foreground path
// of newStartCmd, which delegates to server.Run. Server.Run fails fast
// if the DB env is missing.
func TestNewStartCmdForegroundReachesServerRun(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	writeValidConfig(t)
	t.Setenv("VELOCITY_DB_HOST", "")
	cmd := newStartCmd()
	cmd.Flags().Set("foreground", "true")
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Error("expected server.Run to error without DB env")
	}
}

// TestNewStatusCmdNoPidfile covers the pid==0 branch (calls os.Exit(1) so
// it has to run in a subprocess).
func TestNewStatusCmdNoPidfile(t *testing.T) {
	if dir := os.Getenv("VELOCITY_TEST_STATUS_NOPID"); dir != "" {
		config.SetDir(dir)
		cmd := newStatusCmd()
		_ = cmd.RunE(cmd, nil)
		return
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run", "^TestNewStatusCmdNoPidfile$")
	c.Env = append(os.Environ(), "VELOCITY_TEST_STATUS_NOPID="+dir)
	if err := c.Run(); err == nil {
		t.Error("expected non-zero exit (no pidfile → status calls os.Exit(1))")
	}
}
