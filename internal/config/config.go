package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// StatusBucket maps a canonical bucket to Jira status names. Default is
// the transition target; Aliases also resolve into the bucket on reads.
type StatusBucket struct {
	Default string   `json:"default"`
	Aliases []string `json:"aliases,omitempty"`
}

func (b StatusBucket) All() []string {
	seen := map[string]bool{}
	var out []string
	if b.Default != "" {
		out = append(out, b.Default)
		seen[strings.ToLower(b.Default)] = true
	}
	for _, a := range b.Aliases {
		if a == "" {
			continue
		}
		k := strings.ToLower(a)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}

func (b StatusBucket) Matches(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if strings.ToLower(b.Default) == n {
		return true
	}
	for _, a := range b.Aliases {
		if strings.ToLower(a) == n {
			return true
		}
	}
	return false
}

type TaskStatusMap struct {
	New               StatusBucket `json:"new"`
	Planning          StatusBucket `json:"planning"`
	PlanningFailed    StatusBucket `json:"planning_failed"`
	SubtaskInProgress StatusBucket `json:"subtask_in_progress"`
	Done              StatusBucket `json:"done"`
	Dismissed         StatusBucket `json:"dismissed"`
}

func (s TaskStatusMap) validate() error {
	for name, b := range map[string]StatusBucket{
		"new":                 s.New,
		"planning":            s.Planning,
		"planning_failed":     s.PlanningFailed,
		"subtask_in_progress": s.SubtaskInProgress,
		"done":                s.Done,
		"dismissed":           s.Dismissed,
	} {
		if b.Default == "" {
			return fmt.Errorf("task_status_map.%s.default is required", name)
		}
	}
	return nil
}

type SubtaskStatusMap struct {
	New        StatusBucket `json:"new"`
	InProgress StatusBucket `json:"in_progress"`
	PROpen     StatusBucket `json:"pr_open"`
	CodeFailed StatusBucket `json:"code_failed"`
	Done       StatusBucket `json:"done"`
	Dismissed  StatusBucket `json:"dismissed"`
}

func (s SubtaskStatusMap) validate() error {
	for name, b := range map[string]StatusBucket{
		"new":         s.New,
		"in_progress": s.InProgress,
		"pr_open":     s.PROpen,
		"code_failed": s.CodeFailed,
		"done":        s.Done,
		"dismissed":   s.Dismissed,
	} {
		if b.Default == "" {
			return fmt.Errorf("subtask_status_map.%s.default is required", name)
		}
	}
	return nil
}

// JiraConfig holds Jira instance + status vocabulary config.
// RepoURLField is read off the parent ticket (e.g. "customfield_10050").
type JiraConfig struct {
	BaseURL          string           `json:"base_url"`
	Email            string           `json:"email"`
	ArchitectJiraID  string           `json:"architect_jira_id"`
	DeveloperJiraID  string           `json:"developer_jira_id"`
	RepoURLField     string           `json:"repo_url_field"`
	TaskStatusMap    TaskStatusMap    `json:"task_status_map"`
	SubtaskStatusMap SubtaskStatusMap `json:"subtask_status_map"`
}

func (j JiraConfig) Validate() error {
	if j.BaseURL == "" {
		return errors.New("jira.base_url is required")
	}
	u, err := url.Parse(j.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("jira.base_url is not a valid http(s) URL: %q", j.BaseURL)
	}
	if j.Email == "" {
		return errors.New("jira.email is required")
	}
	if j.ArchitectJiraID == "" {
		return errors.New("jira.architect_jira_id is required")
	}
	if j.DeveloperJiraID == "" {
		return errors.New("jira.developer_jira_id is required")
	}
	if j.RepoURLField == "" {
		return errors.New("jira.repo_url_field is required")
	}
	if err := j.TaskStatusMap.validate(); err != nil {
		return err
	}
	if err := j.SubtaskStatusMap.validate(); err != nil {
		return err
	}
	return nil
}

type LLMRoleConfig struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	AllowedTools   string `json:"allowed_tools"`
	PermissionMode string `json:"permission_mode"`
	TimeoutSec     int    `json:"timeout_sec,omitempty"`
}

type LLMConfig struct {
	Arch LLMRoleConfig `json:"arch"`
	Code LLMRoleConfig `json:"code"`
}

// ServerConfig holds HTTP listener + FIFO dispatch settings.
// MaxConcurrency defaults to 1 (strict serial). Full queue → drop + log.
type ServerConfig struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	MaxConcurrency int    `json:"max_concurrency"`
	QueueSize      int    `json:"queue_size"`
}

// Config is the validated on-disk config.json shape. Secrets live in
// the OS keyring, not here.
type Config struct {
	Jira     JiraConfig   `json:"jira"`
	LLM      LLMConfig    `json:"llm"`
	Server   ServerConfig `json:"server"`
	LogLevel string       `json:"log_level"`
}

func (c Config) Validate() error { return c.Jira.Validate() }

func (c *Config) applyDefaults() {
	if c.LLM.Arch.Provider == "" {
		c.LLM.Arch.Provider = "claude_cli"
	}
	if c.LLM.Arch.Model == "" {
		c.LLM.Arch.Model = "claude-opus-4-6"
	}
	if c.LLM.Arch.AllowedTools == "" {
		c.LLM.Arch.AllowedTools = "Read Glob Grep LS"
	}
	if c.LLM.Arch.PermissionMode == "" {
		c.LLM.Arch.PermissionMode = "bypassPermissions"
	}
	if c.LLM.Code.Provider == "" {
		c.LLM.Code.Provider = "claude_cli"
	}
	if c.LLM.Code.Model == "" {
		c.LLM.Code.Model = "claude-sonnet-4-6"
	}
	if c.LLM.Code.AllowedTools == "" {
		c.LLM.Code.AllowedTools = "Read Write Edit Glob Grep LS MultiEdit Bash"
	}
	if c.LLM.Code.PermissionMode == "" {
		c.LLM.Code.PermissionMode = "bypassPermissions"
	}
	if c.LLM.Arch.TimeoutSec == 0 {
		c.LLM.Arch.TimeoutSec = 600
	}
	if c.LLM.Code.TimeoutSec == 0 {
		c.LLM.Code.TimeoutSec = 1800
	}
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8000
	}
	if c.Server.MaxConcurrency < 1 {
		c.Server.MaxConcurrency = 1
	}
	if c.Server.QueueSize < 1 {
		c.Server.QueueSize = 1024
	}
	if c.LogLevel == "" {
		c.LogLevel = "INFO"
	}
	c.Jira.BaseURL = strings.TrimRight(c.Jira.BaseURL, "/")
}

var (
	current   *Config
	loadError string
)

// loadConfig is called by SetDir. Missing file degrades to setup mode;
// invalid JSON / failed validation → loadError set.
func loadConfig() {
	current = nil
	loadError = ""

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		loadError = err.Error()
		return
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		loadError = fmt.Sprintf("invalid JSON in config file: %v", err)
		return
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		loadError = fmt.Sprintf("invalid config in %s: %v", ConfigPath(), err)
		return
	}
	current = &cfg
}

func Get() *Config { return current }

func LoadError() string { return loadError }

// Reload re-reads config.json from disk.
func Reload() error {
	loadConfig()
	if current == nil && loadError != "" {
		return errors.New(loadError)
	}
	if current == nil {
		return errors.New("config.json not found at " + ConfigPath())
	}
	return nil
}

func Save(cfg *Config) error {
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := EnsureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(ConfigPath(), data, 0o644); err != nil {
		return err
	}
	current = cfg
	loadError = ""
	return nil
}
