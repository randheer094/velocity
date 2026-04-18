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
	if DataDir != filepath.Join(dir, "data") {
		t.Errorf("DataDir = %q", DataDir)
	}
	if WorkspaceDir != filepath.Join(dir, "workspace") {
		t.Errorf("WorkspaceDir = %q", WorkspaceDir)
	}
	if ConfigPath() != filepath.Join(dir, "config.json") {
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
}

func TestEnsureRuntimeDirs(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())

	if err := EnsureRuntimeDirs(); err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}
	for _, d := range []string{AgentDir, DataDir, WorkspaceDir} {
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
	if got := expandHome(""); got != "" {
		t.Errorf("empty path expanded to %q", got)
	}
	if got := expandHome("/abs"); got != "/abs" {
		t.Errorf("absolute path changed: %q", got)
	}
	home, _ := os.UserHomeDir()
	if got := expandHome("~/foo"); !strings.HasPrefix(got, home) {
		t.Errorf("~ not expanded: %q", got)
	}
}
