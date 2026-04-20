package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

const cfgTmpl = `{
  "jira": {
    "base_url": "https://example.atlassian.net",
    "email": "a@b.c",
    "architect_jira_id": "arch-id",
    "developer_jira_id": "dev-id",
    "repo_url_field": "customfield_repo",
    "task_status_map": {
      "new": {"default": "To Do"},
      "planning": {"default": "Planning"},
      "planning_failed": {"default": "Planning Failed"},
      "coding": {"default": "In Progress"},
      "done": {"default": "Done", "aliases": ["Dismissed"]}
    },
    "subtask_status_map": {
      "new": {"default": "To Do"},
      "coding": {"default": "Dev In Progress"},
      "coding_failed": {"default": "Dev Failed"},
      "in_review": {"default": "In Review"},
      "done": {"default": "Done", "aliases": ["Dismissed"]}
    }
  },
  "server": {"host": "127.0.0.1", "port": %d, "max_concurrency": 1, "queue_size": 4}
}`

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func TestRunMissingConfig(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	if err := Run(); err == nil {
		t.Error("expected error from missing config")
	}
}

// TestRunEnsureRuntimeDirsFails covers the EnsureRuntimeDirs error branch.
// SetDir derives WorkspaceDir = AgentDir/workspace; placing a regular file
// at that path makes MkdirAll fail with ENOTDIR.
func TestRunEnsureRuntimeDirsFails(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf(cfgTmpl, freePort(t))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)
	if err := Run(); err == nil || !contains(err.Error(), "ensure runtime dirs") {
		t.Errorf("expected ensure runtime dirs error, got %v", err)
	}
}

// TestRunDBStartFails covers the db.Start error branch: valid config,
// runtime dirs OK, but DB env points at an unreachable target.
func TestRunDBStartFails(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf(cfgTmpl, freePort(t))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)
	t.Setenv(config.DBHostEnv, "")
	if err := Run(); err == nil || !contains(err.Error(), "start db") {
		t.Errorf("expected start db error, got %v", err)
	}
}

// TestRunListenErrorReturned covers the errCh-receive branch when
// ListenAndServe fails (port already bound).
func TestRunListenErrorReturned(t *testing.T) {
	if os.Getenv(config.DBHostEnv) == "" || os.Getenv(config.DBPasswordEnv) == "" {
		t.Skipf("set %s and %s to run", config.DBHostEnv, config.DBPasswordEnv)
	}
	// Hold a port so the server's ListenAndServe sees EADDRINUSE.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	cfg := fmt.Sprintf(cfgTmpl, port)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)

	done := make(chan error, 1)
	go func() { done <- Run() }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected listen error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after listen failure")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRunStartsAndShutdown(t *testing.T) {
	if os.Getenv(config.DBHostEnv) == "" || os.Getenv(config.DBPasswordEnv) == "" {
		t.Skipf("set %s and %s to run", config.DBHostEnv, config.DBPasswordEnv)
	}
	dir := t.TempDir()
	port := freePort(t)
	cfg := fmt.Sprintf(cfgTmpl, port)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)

	done := make(chan error, 1)
	go func() { done <- Run() }()

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Trigger graceful shutdown via SIGTERM.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}
}
