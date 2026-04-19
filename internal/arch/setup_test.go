package arch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

const cfgJSON = `{
  "jira": {
    "base_url": "https://example.atlassian.net",
    "email": "a@b.c",
    "architect_jira_id": "arch-id",
    "developer_jira_id": "dev-id",
    "repo_url_field": "customfield_repo",
    "project_keys": ["PROJ"],
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
  }
}`

// setupConfig writes a valid config to a tempdir and points config at it.
// Returns a cleanup that restores the TestMain config dir so subsequent
// tests see a populated config.
func setupConfig(t *testing.T) func() {
	t.Helper()
	prev := config.AgentDir
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)
	if config.Get() == nil {
		t.Fatalf("config not loaded: %s", config.LoadError())
	}
	return func() { config.SetDir(prev) }
}
