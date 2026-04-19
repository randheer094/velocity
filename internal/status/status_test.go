package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

func writeConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := `{
  "jira": {
    "base_url": "https://example.atlassian.net",
    "email": "a@b.c",
    "architect_jira_id": "arch",
    "developer_jira_id": "dev",
    "repo_url_field": "customfield_1",
    "project_keys": ["PROJ"],
    "task_status_map": {
      "new": {"default": "To Do"},
      "planning": {"default": "Planning"},
      "planning_failed": {"default": "Planning Failed"},
      "subtask_in_progress": {"default": "In Progress"},
      "done": {"default": "Done", "aliases": ["Closed"]},
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
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetDir(dir)
	if config.Get() == nil {
		t.Fatalf("config not loaded: %s", config.LoadError())
	}
}

func resetConfig(t *testing.T) {
	t.Helper()
	config.SetDir(t.TempDir())
}

func TestTaskJiraName(t *testing.T) {
	writeConfig(t)
	cases := map[Canonical]string{
		New:               "To Do",
		Planning:          "Planning",
		PlanningFailed:    "Planning Failed",
		SubtaskInProgress: "In Progress",
		Done:              "Done",
		Dismissed:         "Dismissed",
		PROpen:            "",
	}
	for c, want := range cases {
		if got := TaskJiraName(c); got != want {
			t.Errorf("TaskJiraName(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestSubtaskJiraName(t *testing.T) {
	writeConfig(t)
	cases := map[Canonical]string{
		New:        "To Do",
		InProgress: "In Progress",
		PROpen:     "In Review",
		CodeFailed: "Dev Failed",
		Done:       "Done",
		Dismissed:  "Dismissed",
		Planning:   "",
	}
	for c, want := range cases {
		if got := SubtaskJiraName(c); got != want {
			t.Errorf("SubtaskJiraName(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestTaskCanonical(t *testing.T) {
	writeConfig(t)
	cases := map[string]Canonical{
		"To Do":           New,
		"Planning":        Planning,
		"Planning Failed": PlanningFailed,
		"In Progress":     SubtaskInProgress,
		"Done":            Done,
		"Closed":          Done,
		"Dismissed":       Dismissed,
		"unknown":         "",
	}
	for in, want := range cases {
		if got := TaskCanonical(in); got != want {
			t.Errorf("TaskCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubtaskCanonical(t *testing.T) {
	writeConfig(t)
	cases := map[string]Canonical{
		"To Do":       New,
		"In Progress": InProgress,
		"In Review":   PROpen,
		"Dev Failed":  CodeFailed,
		"Done":        Done,
		"Dismissed":   Dismissed,
		"unknown":     "",
	}
	for in, want := range cases {
		if got := SubtaskCanonical(in); got != want {
			t.Errorf("SubtaskCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNoConfigReturnsEmpty(t *testing.T) {
	resetConfig(t)
	if got := TaskJiraName(Done); got != "" {
		t.Errorf("TaskJiraName with no config = %q, want empty", got)
	}
	if got := SubtaskJiraName(Done); got != "" {
		t.Errorf("SubtaskJiraName with no config = %q, want empty", got)
	}
	if got := TaskCanonical("Done"); got != "" {
		t.Errorf("TaskCanonical with no config = %q, want empty", got)
	}
	if got := SubtaskCanonical("Done"); got != "" {
		t.Errorf("SubtaskCanonical with no config = %q, want empty", got)
	}
}
