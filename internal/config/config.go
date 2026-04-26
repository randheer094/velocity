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

// MatchesAlias reports whether name matches one of the bucket's aliases
// (ignoring Default). Used to distinguish dismissal transitions from
// regular Done within the same done bucket.
func (b StatusBucket) MatchesAlias(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, a := range b.Aliases {
		if strings.ToLower(a) == n {
			return true
		}
	}
	return false
}

// FirstAlias returns the first non-empty alias, or "" if none.
func (b StatusBucket) FirstAlias() string {
	for _, a := range b.Aliases {
		if a != "" {
			return a
		}
	}
	return ""
}

type TaskStatusMap struct {
	New            StatusBucket `yaml:"new"`
	Planning       StatusBucket `yaml:"planning"`
	PlanningFailed StatusBucket `yaml:"planning_failed"`
	Coding         StatusBucket `yaml:"coding"`
	Done           StatusBucket `yaml:"done"`
}

func (s TaskStatusMap) validate() error {
	for name, b := range map[string]StatusBucket{
		"new":             s.New,
		"planning":        s.Planning,
		"planning_failed": s.PlanningFailed,
		"coding":          s.Coding,
		"done":            s.Done,
	} {
		if b.Default == "" {
			return fmt.Errorf("task_status_map.%s.default is required", name)
		}
	}
	return nil
}

type SubtaskStatusMap struct {
	New          StatusBucket `yaml:"new"`
	Coding       StatusBucket `yaml:"coding"`
	CodingFailed StatusBucket `yaml:"coding_failed"`
	InReview     StatusBucket `yaml:"in_review"`
	Done         StatusBucket `yaml:"done"`
}

func (s SubtaskStatusMap) validate() error {
	for name, b := range map[string]StatusBucket{
		"new":           s.New,
		"coding":        s.Coding,
		"coding_failed": s.CodingFailed,
		"in_review":     s.InReview,
		"done":          s.Done,
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
		"new":             m.New,
		"planning":        m.Planning,
		"planning_failed": m.PlanningFailed,
		"coding":          m.Coding,
		"done":            m.Done,
	}
}

func subtaskBucketMap(m SubtaskStatusMap) map[string]StatusBucket {
	return map[string]StatusBucket{
		"new":           m.New,
		"coding":        m.Coding,
		"coding_failed": m.CodingFailed,
		"in_review":     m.InReview,
		"done":          m.Done,
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
// MaxConcurrency gates the llm queue (arch.Run / code.Run /
// code.Iterate); the ops queue is always 1 worker. Defaults to 1
// (strict serial LLM work). Full queue → drop + log per queue.
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	QueueSize      int    `yaml:"queue_size"`
}

// Config is the validated on-disk config.yaml shape. Secrets and all
// Postgres connection fields are sourced from env vars (see secrets.go),
// not written to disk.
type Config struct {
	Jira   JiraConfig   `yaml:"jira"`
	LLM    LLMConfig    `yaml:"llm"`
	Server ServerConfig `yaml:"server"`
}

func (c Config) Validate() error {
	return c.Jira.Validate()
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
