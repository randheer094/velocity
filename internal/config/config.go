package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// StatusBucket maps a canonical bucket to Jira status names. Default is
// the transition target; Aliases also resolve into the bucket on reads.
type StatusBucket struct {
	Default string   `yaml:"default"`
	Aliases []string `yaml:"aliases,omitempty"`
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
	New               StatusBucket `yaml:"new"`
	Planning          StatusBucket `yaml:"planning"`
	PlanningFailed    StatusBucket `yaml:"planning_failed"`
	SubtaskInProgress StatusBucket `yaml:"subtask_in_progress"`
	Done              StatusBucket `yaml:"done"`
	Dismissed         StatusBucket `yaml:"dismissed"`
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
	New        StatusBucket `yaml:"new"`
	InProgress StatusBucket `yaml:"in_progress"`
	PROpen     StatusBucket `yaml:"pr_open"`
	CodeFailed StatusBucket `yaml:"code_failed"`
	Done       StatusBucket `yaml:"done"`
	Dismissed  StatusBucket `yaml:"dismissed"`
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
	BaseURL          string           `yaml:"base_url"`
	Email            string           `yaml:"email"`
	ArchitectJiraID  string           `yaml:"architect_jira_id"`
	DeveloperJiraID  string           `yaml:"developer_jira_id"`
	RepoURLField     string           `yaml:"repo_url_field"`
	TaskStatusMap    TaskStatusMap    `yaml:"task_status_map"`
	SubtaskStatusMap SubtaskStatusMap `yaml:"subtask_status_map"`
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
	if err := validateNoOverlap("task_status_map", taskBucketMap(j.TaskStatusMap)); err != nil {
		return err
	}
	if err := validateNoOverlap("subtask_status_map", subtaskBucketMap(j.SubtaskStatusMap)); err != nil {
		return err
	}
	return nil
}

func taskBucketMap(m TaskStatusMap) map[string]StatusBucket {
	return map[string]StatusBucket{
		"new":                 m.New,
		"planning":            m.Planning,
		"planning_failed":     m.PlanningFailed,
		"subtask_in_progress": m.SubtaskInProgress,
		"done":                m.Done,
		"dismissed":           m.Dismissed,
	}
}

func subtaskBucketMap(m SubtaskStatusMap) map[string]StatusBucket {
	return map[string]StatusBucket{
		"new":         m.New,
		"in_progress": m.InProgress,
		"pr_open":     m.PROpen,
		"code_failed": m.CodeFailed,
		"done":        m.Done,
		"dismissed":   m.Dismissed,
	}
}

// validateNoOverlap rejects a status name appearing in two buckets of the
// same workflow. Same name across task and subtask workflows is allowed.
func validateNoOverlap(scope string, buckets map[string]StatusBucket) error {
	owner := map[string]string{}
	for bucket, b := range buckets {
		for _, name := range b.All() {
			k := strings.ToLower(name)
			if prev, dup := owner[k]; dup && prev != bucket {
				return fmt.Errorf("%s: status %q appears in both %s and %s", scope, name, prev, bucket)
			}
			owner[k] = bucket
		}
	}
	return nil
}

type LLMRoleConfig struct {
	Provider       string `yaml:"provider"`
	Model          string `yaml:"model"`
	AllowedTools   string `yaml:"allowed_tools"`
	PermissionMode string `yaml:"permission_mode"`
	TimeoutSec     int    `yaml:"timeout_sec,omitempty"`
}

type LLMConfig struct {
	Arch LLMRoleConfig `yaml:"arch"`
	Code LLMRoleConfig `yaml:"code"`
}

// ServerConfig holds HTTP listener + FIFO dispatch settings.
// MaxConcurrency defaults to 1 (strict serial). Full queue → drop + log.
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	QueueSize      int    `yaml:"queue_size"`
}

// DatabaseConfig names the external Postgres velocity connects to.
// Host + password come from DBHostEnv / DBPasswordEnv, never config.yaml.
type DatabaseConfig struct {
	Port    int    `yaml:"port"`
	User    string `yaml:"user"`
	Name    string `yaml:"name"`
	SSLMode string `yaml:"sslmode"`
}

func (d DatabaseConfig) Validate() error {
	if d.Port <= 0 || d.Port > 65535 {
		return fmt.Errorf("database.port out of range: %d", d.Port)
	}
	if d.User == "" {
		return errors.New("database.user is required")
	}
	if d.Name == "" {
		return errors.New("database.name is required")
	}
	return nil
}

// Config is the validated on-disk config.yaml shape. Secrets are
// sourced from env vars (see secrets.go), not written to disk.
type Config struct {
	Jira     JiraConfig     `yaml:"jira"`
	LLM      LLMConfig      `yaml:"llm"`
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	LogLevel string         `yaml:"log_level"`
}

func (c Config) Validate() error {
	if err := c.Jira.Validate(); err != nil {
		return err
	}
	return c.Database.Validate()
}

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
	if c.Database.Port == 0 {
		c.Database.Port = 5432
	}
	if c.Database.User == "" {
		c.Database.User = "velocity"
	}
	if c.Database.Name == "" {
		c.Database.Name = "velocity"
	}
	if c.Database.SSLMode == "" {
		c.Database.SSLMode = "disable"
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
// invalid YAML / failed validation → loadError set.
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
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		loadError = fmt.Sprintf("invalid YAML in config file: %v", err)
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

// Reload re-reads config.yaml from disk.
func Reload() error {
	loadConfig()
	if current == nil && loadError != "" {
		return errors.New(loadError)
	}
	if current == nil {
		return errors.New("config.yaml not found at " + ConfigPath())
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
	data, err := yaml.Marshal(cfg)
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
