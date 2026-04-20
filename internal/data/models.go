// Package data holds the Plan / CodeTask value types (keyed on
// Jira issue keys). Persistence lives in internal/db.
package data

import (
	"fmt"
	"regexp"
	"time"
)

var jiraKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)

func ValidJiraKey(s string) bool { return jiraKeyRe.MatchString(s) }

// PlannedTask is one leaf ticket emitted by arch. Tasks live inside
// waves; wave position encodes ordering, so there is no separate id.
type PlannedTask struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	JiraKey     string `json:"jira_key,omitempty"`
}

// Wave groups tasks safe to run in parallel; waves are sequential.
type Wave struct {
	Tasks []PlannedTask `json:"tasks"`
}

type PlanStatus string

const (
	PlanNew            PlanStatus = "new"
	PlanPlanning       PlanStatus = "planning"
	PlanPlanningFailed PlanStatus = "planning_failed"
	PlanCoding         PlanStatus = "coding"
	PlanDone           PlanStatus = "done"
)

// Plan is arch's full output, persisted per parent issue.
type Plan struct {
	ParentJiraKey  string     `json:"parent_jira_key"`
	Name           string     `json:"name"`
	RepoURL        string     `json:"repo_url"`
	Waves          []Wave     `json:"waves"`
	ActiveWaveIdx  int        `json:"active_wave_idx"`
	Status         PlanStatus `json:"status"`
	JiraStatus     string     `json:"jira_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	LastErrorStage string     `json:"last_error_stage,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (p Plan) Validate() error {
	if !ValidJiraKey(p.ParentJiraKey) {
		return fmt.Errorf("invalid parent jira key: %q", p.ParentJiraKey)
	}
	for i, w := range p.Waves {
		for j, t := range w.Tasks {
			if t.Title == "" {
				return fmt.Errorf("waves[%d].tasks[%d].title is required", i, j)
			}
		}
	}
	return nil
}

type CodeStatus string

const (
	CodeNew          CodeStatus = "new"
	CodeCoding       CodeStatus = "coding"
	CodeCodingFailed CodeStatus = "coding_failed"
	CodeInReview     CodeStatus = "in_review"
	CodeDone         CodeStatus = "done"
)

// CodeTask is the persisted record for a sub-task's coding run.
type CodeTask struct {
	IssueKey       string     `json:"issue_key"`
	ParentJiraKey  string     `json:"parent_jira_key"`
	RepoURL        string     `json:"repo_url"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Branch         string     `json:"branch,omitempty"`
	PRURL          string     `json:"pr_url,omitempty"`
	Status         CodeStatus `json:"status"`
	JiraStatus     string     `json:"jira_status,omitempty"`
	Error          string     `json:"error,omitempty"`
	LastErrorStage string     `json:"last_error_stage,omitempty"`
	FailedAt       *time.Time `json:"failed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (c CodeTask) Validate() error {
	if !ValidJiraKey(c.IssueKey) {
		return fmt.Errorf("invalid issue_key: %q", c.IssueKey)
	}
	if c.RepoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	return nil
}
