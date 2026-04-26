package arch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/prompts"
)

// loadFixturePrompts installs a small in-memory prompt set sufficient
// for the arch package's tests. All call sites that hit the LLM path
// must use this so render errors don't masquerade as real failures.
func loadFixturePrompts(t *testing.T) {
	t.Helper()
	prompts.SetForTest(t, map[string]string{
		"arch_plan":    "{{.PlanBegin}} parent={{.ParentKey}} req={{.Requirement}} {{.PlanEnd}}",
		"failure_jira": "Velocity {{.Role}} failed at stage {{.Stage}}: {{.Message}}",
	})
}

// resetPromptsForTest tears down any installed prompts so the no-store
// fallback path can be exercised.
func resetPromptsForTest(t *testing.T) {
	t.Helper()
	prompts.ResetForTest(t)
}

const cfgJSON = `{
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
