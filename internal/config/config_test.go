package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validJira() JiraConfig {
	return JiraConfig{
		BaseURL:         "https://example.atlassian.net",
		Email:           "ops@example.com",
		ArchitectJiraID: "arch-id",
		DeveloperJiraID: "dev-id",
		RepoURLField:    "customfield_10050",
		TaskStatusMap: TaskStatusMap{
			New:               StatusBucket{Default: "To Do"},
			Planning:          StatusBucket{Default: "Planning"},
			PlanningFailed:    StatusBucket{Default: "Planning Failed"},
			SubtaskInProgress: StatusBucket{Default: "In Progress"},
			Done:              StatusBucket{Default: "Done"},
			Dismissed:         StatusBucket{Default: "Dismissed"},
		},
		SubtaskStatusMap: SubtaskStatusMap{
			New:        StatusBucket{Default: "To Do"},
			InProgress: StatusBucket{Default: "In Progress"},
			PROpen:     StatusBucket{Default: "In Review"},
			CodeFailed: StatusBucket{Default: "Dev Failed"},
			Done:       StatusBucket{Default: "Done"},
			Dismissed:  StatusBucket{Default: "Dismissed"},
		},
	}
}

func TestJiraConfigValidate(t *testing.T) {
	good := validJira()
	if err := good.Validate(); err != nil {
		t.Fatalf("good config failed: %v", err)
	}

	cases := map[string]func(*JiraConfig){
		"missing base url":   func(j *JiraConfig) { j.BaseURL = "" },
		"bad base url":       func(j *JiraConfig) { j.BaseURL = "ftp://nope" },
		"missing email":      func(j *JiraConfig) { j.Email = "" },
		"missing arch id":    func(j *JiraConfig) { j.ArchitectJiraID = "" },
		"missing dev id":     func(j *JiraConfig) { j.DeveloperJiraID = "" },
		"missing repo field": func(j *JiraConfig) { j.RepoURLField = "" },
		"missing task new":   func(j *JiraConfig) { j.TaskStatusMap.New = StatusBucket{} },
		"missing sub done":   func(j *JiraConfig) { j.SubtaskStatusMap.Done = StatusBucket{} },
		"task overlap": func(j *JiraConfig) {
			j.TaskStatusMap.Planning = StatusBucket{Default: "Same"}
			j.TaskStatusMap.PlanningFailed = StatusBucket{Default: "Same"}
		},
		"subtask overlap via alias": func(j *JiraConfig) {
			j.SubtaskStatusMap.PROpen = StatusBucket{Default: "In Review", Aliases: []string{"In Progress"}}
		},
	}
	for name, mut := range cases {
		j := validJira()
		mut(&j)
		if err := j.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	c := &Config{Jira: validJira()}
	c.applyDefaults()

	if c.LLM.Arch.Provider != "claude_cli" || c.LLM.Code.Provider != "claude_cli" {
		t.Errorf("default provider missing: %+v", c.LLM)
	}
	if c.LLM.Arch.Model == "" || c.LLM.Code.Model == "" {
		t.Errorf("default model missing")
	}
	if c.LLM.Arch.AllowedTools == "" || c.LLM.Code.AllowedTools == "" {
		t.Errorf("default tools missing")
	}
	if c.LLM.Arch.PermissionMode != "bypassPermissions" || c.LLM.Code.PermissionMode != "bypassPermissions" {
		t.Errorf("default perm mode missing")
	}
	if c.LLM.Arch.TimeoutSec != 600 || c.LLM.Code.TimeoutSec != 1800 {
		t.Errorf("default timeouts wrong: %+v", c.LLM)
	}
	if c.Server.Host != "0.0.0.0" || c.Server.Port != 8000 {
		t.Errorf("default server addr wrong: %+v", c.Server)
	}
	if c.Server.MaxConcurrency != 1 || c.Server.QueueSize != 1024 {
		t.Errorf("default server queue wrong: %+v", c.Server)
	}

	// Existing values not overwritten
	c2 := &Config{
		Jira: validJira(),
		LLM: LLMConfig{
			Arch: LLMRoleConfig{Provider: "X", Model: "M", AllowedTools: "T", PermissionMode: "P", TimeoutSec: 5},
			Code: LLMRoleConfig{Provider: "Y", Model: "N", AllowedTools: "U", PermissionMode: "Q", TimeoutSec: 7},
		},
		Server: ServerConfig{Host: "h", Port: 9, MaxConcurrency: 4, QueueSize: 16},
	}
	c2.applyDefaults()
	if c2.LLM.Arch.Provider != "X" || c2.LLM.Code.Provider != "Y" {
		t.Errorf("provider overwritten: %+v", c2.LLM)
	}
	if c2.Server.Port != 9 || c2.Server.MaxConcurrency != 4 {
		t.Errorf("server overwritten: %+v", c2.Server)
	}

	// trailing slash trimmed
	c3 := &Config{Jira: validJira()}
	c3.Jira.BaseURL = "https://example.atlassian.net/"
	c3.applyDefaults()
	if c3.Jira.BaseURL != "https://example.atlassian.net" {
		t.Errorf("base URL not trimmed: %q", c3.Jira.BaseURL)
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())

	cfg := &Config{Jira: validJira()}
	if err := Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if Get() == nil {
		t.Fatalf("Get nil after Save")
	}

	// Reload should succeed
	if err := Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if Get().Jira.Email != "ops@example.com" {
		t.Errorf("reload lost data: %+v", Get().Jira)
	}
}

func TestCrossWorkflowOverlapIsAllowed(t *testing.T) {
	j := validJira()
	// "In Progress" appears in task SubtaskInProgress and subtask InProgress.
	// That's explicitly allowed — different workflows, different meanings.
	if err := j.Validate(); err != nil {
		t.Fatalf("cross-workflow overlap should validate: %v", err)
	}
}

func TestSaveValidationFails(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())

	bad := &Config{}
	if err := Save(bad); err == nil {
		t.Errorf("expected validation error")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	SetDir(dir)
	defer SetDir(t.TempDir())
	if Get() != nil {
		t.Errorf("expected nil config when file missing")
	}
	if LoadError() != "" {
		t.Errorf("expected empty load error for missing file: %q", LoadError())
	}
	if err := Reload(); err == nil {
		t.Errorf("Reload should fail when file missing")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("foo: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetDir(dir)
	defer SetDir(t.TempDir())
	if Get() != nil {
		t.Errorf("expected nil config for invalid YAML")
	}
	if !strings.Contains(LoadError(), "invalid YAML") {
		t.Errorf("expected invalid YAML error, got %q", LoadError())
	}
}

func TestLoadConfigInvalidValidation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("jira: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetDir(dir)
	defer SetDir(t.TempDir())
	if Get() != nil {
		t.Errorf("expected nil config for invalid validation")
	}
	if LoadError() == "" {
		t.Errorf("expected non-empty load error")
	}
}
