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
    "task_status_map": {
      "new": {"default": "To Do"},
      "planning": {"default": "Planning"},
      "planning_failed": {"default": "Planning Failed"},
      "coding": {"default": "In Progress"},
      "done": {"default": "Done", "aliases": ["Closed", "Dismissed"]}
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
		New:            "To Do",
		Planning:       "Planning",
		PlanningFailed: "Planning Failed",
		Coding:         "In Progress",
		Done:           "Done",
		InReview:       "",
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
		New:          "To Do",
		Coding:       "Dev In Progress",
		InReview:     "In Review",
		CodingFailed: "Dev Failed",
		Done:         "Done",
		Planning:     "",
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
		"In Progress":     Coding,
		"Done":            Done,
		"Closed":          Done,
		"Dismissed":       Done,
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
		"To Do":           New,
		"Dev In Progress": Coding,
		"In Review":       InReview,
		"Dev Failed":      CodingFailed,
		"Done":            Done,
		"Dismissed":       Done,
		"unknown":         "",
	}
	for in, want := range cases {
		if got := SubtaskCanonical(in); got != want {
			t.Errorf("SubtaskCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDismissAliasHelpers(t *testing.T) {
	writeConfig(t)
	if !IsTaskDismissAlias("Dismissed") {
		t.Error("IsTaskDismissAlias(Dismissed) = false, want true")
	}
	if IsTaskDismissAlias("Done") {
		t.Error("IsTaskDismissAlias(Done) = true, want false (default, not alias)")
	}
	if !IsSubtaskDismissAlias("Dismissed") {
		t.Error("IsSubtaskDismissAlias(Dismissed) = false, want true")
	}
	if IsSubtaskDismissAlias("Done") {
		t.Error("IsSubtaskDismissAlias(Done) = true, want false")
	}
	if got := SubtaskDismissJiraName(); got != "Dismissed" {
		t.Errorf("SubtaskDismissJiraName = %q, want %q", got, "Dismissed")
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
