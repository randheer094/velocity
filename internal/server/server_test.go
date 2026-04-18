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
      "subtask_in_progress": {"default": "In Progress"},
      "done": {"default": "Done"},
      "dismissed": {"default": "Dismissed"}
    },
    "subtask_status_map": {
      "new": {"default": "To Do"},
      "in_progress": {"default": "In Progress"},
      "pr_open": {"default": "In Review"},
      "code_failed": {"default": "Dev Failed"},
      "done": {"default": "Done"},
      "dismissed": {"default": "Dismissed"}
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

func TestRunStartsAndShutdown(t *testing.T) {
	if os.Getenv("SKIP_EMBEDDED_PG") != "" {
		t.Skip("embedded pg disabled")
	}
	dir := t.TempDir()
	port := freePort(t)
	cfg := fmt.Sprintf(cfgTmpl, port)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
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
