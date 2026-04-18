// Package status resolves canonical buckets to/from Jira status names.
package status

import (
	"github.com/randheer094/velocity/internal/config"
)

// Canonical is a lifecycle bucket, independent of Jira status names.
type Canonical string

const (
	New               Canonical = "new"
	Planning          Canonical = "planning"
	PlanningFailed    Canonical = "planning_failed"
	SubtaskInProgress Canonical = "subtask_in_progress"
	InProgress        Canonical = "in_progress"
	PROpen            Canonical = "pr_open"
	CodeFailed        Canonical = "code_failed"
	Done              Canonical = "done"
	Dismissed         Canonical = "dismissed"
)

// TaskJiraName returns the default Jira name for a parent-task bucket.
func TaskJiraName(c Canonical) string {
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	m := cfg.Jira.TaskStatusMap
	switch c {
	case New:
		return m.New.Default
	case Planning:
		return m.Planning.Default
	case PlanningFailed:
		return m.PlanningFailed.Default
	case SubtaskInProgress:
		return m.SubtaskInProgress.Default
	case Done:
		return m.Done.Default
	case Dismissed:
		return m.Dismissed.Default
	}
	return ""
}

// SubtaskJiraName returns the default Jira name for a sub-task bucket.
func SubtaskJiraName(c Canonical) string {
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	m := cfg.Jira.SubtaskStatusMap
	switch c {
	case New:
		return m.New.Default
	case InProgress:
		return m.InProgress.Default
	case PROpen:
		return m.PROpen.Default
	case CodeFailed:
		return m.CodeFailed.Default
	case Done:
		return m.Done.Default
	case Dismissed:
		return m.Dismissed.Default
	}
	return ""
}

// SubtaskCanonical maps a Jira status name back to its bucket.
// Matches default and aliases (case-insensitive).
func SubtaskCanonical(name string) Canonical {
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	m := cfg.Jira.SubtaskStatusMap
	switch {
	case m.New.Matches(name):
		return New
	case m.InProgress.Matches(name):
		return InProgress
	case m.PROpen.Matches(name):
		return PROpen
	case m.CodeFailed.Matches(name):
		return CodeFailed
	case m.Done.Matches(name):
		return Done
	case m.Dismissed.Matches(name):
		return Dismissed
	}
	return ""
}

// TaskCanonical maps a Jira status name back to its parent-task bucket.
func TaskCanonical(name string) Canonical {
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	m := cfg.Jira.TaskStatusMap
	switch {
	case m.New.Matches(name):
		return New
	case m.Planning.Matches(name):
		return Planning
	case m.PlanningFailed.Matches(name):
		return PlanningFailed
	case m.SubtaskInProgress.Matches(name):
		return SubtaskInProgress
	case m.Done.Matches(name):
		return Done
	case m.Dismissed.Matches(name):
		return Dismissed
	}
	return ""
}

// IssueInfo is the minimal snapshot the orchestrator works with.
type IssueInfo struct {
	Key               string
	Status            string
	AssigneeAccountID string
}
