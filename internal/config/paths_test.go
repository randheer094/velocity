package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetDirAndPaths(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())

	if AgentDir != dir {
		t.Errorf("AgentDir = %q, want %q", AgentDir, dir)
	}
	if WorkspaceDir != filepath.Join(dir, "workspace") {
		t.Errorf("WorkspaceDir = %q", WorkspaceDir)
	}
	if ConfigPath() != filepath.Join(dir, "config.yaml") {
		t.Errorf("ConfigPath = %q", ConfigPath())
	}
	if PidfilePath() != filepath.Join(dir, "daemon.pid") {
		t.Errorf("PidfilePath = %q", PidfilePath())
	}
	if LogfilePath() != filepath.Join(dir, "daemon.log") {
		t.Errorf("LogfilePath = %q", LogfilePath())
	}
	if WorkspacePath("ABC-1") != filepath.Join(dir, "workspace", "ABC-1") {
		t.Errorf("WorkspacePath wrong: %q", WorkspacePath("ABC-1"))
	}
	if ResourcesDir() != filepath.Join(dir, "resources") {
		t.Errorf("ResourcesDir = %q", ResourcesDir())
	}
	if ResourcesVersionPath() != filepath.Join(dir, "resources", "VERSION") {
		t.Errorf("ResourcesVersionPath = %q", ResourcesVersionPath())
	}
}

func TestEnsureRuntimeDirs(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())

	if err := EnsureRuntimeDirs(); err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}
	for _, d := range []string{AgentDir, WorkspaceDir} {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			t.Errorf("expected dir at %q: %v", d, err)
		}
	}
}

func TestEnsureRuntimeDirsError(t *testing.T) {
	dir := t.TempDir()
	// Make the parent unwritable so MkdirAll fails for the subdir.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetDir(filepath.Join(blocker, "nested"))
	defer SetDir(t.TempDir())

	if err := EnsureRuntimeDirs(); err == nil {
		t.Error("expected error when parent is a regular file")
	}
}

func TestEnsureDir(t *testing.T) {
	dir := t.TempDir()
	SetDir(filepath.Join(dir, "nested"))
	defer SetDir(t.TempDir())
	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if _, err := os.Stat(AgentDir); err != nil {
		t.Errorf("expected dir: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	got, err := expandHome("")
	if err != nil || got != "" {
		t.Errorf("empty path expanded to %q (err=%v)", got, err)
	}
	got, err = expandHome("/abs")
	if err != nil || got != "/abs" {
		t.Errorf("absolute path changed: %q (err=%v)", got, err)
	}
	home, _ := os.UserHomeDir()
	got, err = expandHome("~/foo")
	if err != nil || !strings.HasPrefix(got, home) {
		t.Errorf("~ not expanded: %q (err=%v)", got, err)
	}
}

func TestExpandHomeError(t *testing.T) {
	t.Setenv("HOME", "")
	// On non-Linux, os.UserHomeDir consults different env vars; this
	// test is best-effort. Skip if HOME being empty doesn't fail.
	if _, err := expandHome("~/foo"); err == nil {
		t.Skip("UserHomeDir returned a path even with HOME unset")
	}
}

func TestSetDirReturnsErrorOnHomeFailure(t *testing.T) {
	t.Setenv("HOME", "")
	if err := SetDir("~/something"); err == nil {
		t.Skip("UserHomeDir returned a path even with HOME unset")
	}
}
