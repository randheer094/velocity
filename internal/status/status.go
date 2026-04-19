// Package status resolves canonical buckets to/from Jira status names.
package status

import (
	"github.com/randheer094/velocity/internal/config"
)

// Canonical is a lifecycle bucket, independent of Jira status names.
type Canonical string

const (
	New            Canonical = "new"
	Planning       Canonical = "planning"
	PlanningFailed Canonical = "planning_failed"
	Coding         Canonical = "coding"
	CodingFailed   Canonical = "coding_failed"
	InReview       Canonical = "in_review"
	Done           Canonical = "done"
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
	case Coding:
		return m.Coding.Default
	case Done:
		return m.Done.Default
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
	case Coding:
		return m.Coding.Default
	case CodingFailed:
		return m.CodingFailed.Default
	case InReview:
		return m.InReview.Default
	case Done:
		return m.Done.Default
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
	case m.Coding.Matches(name):
		return Coding
	case m.CodingFailed.Matches(name):
		return CodingFailed
	case m.InReview.Matches(name):
		return InReview
	case m.Done.Matches(name):
		return Done
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
	case m.Coding.Matches(name):
		return Coding
	case m.Done.Matches(name):
		return Done
	}
	return ""
}

// IsTaskDismissAlias reports whether name is a dismissal alias on the
// task done bucket (i.e. matches an alias, not the default). Dismissal
// is represented as an alias of Done; only parent dismissals cascade.
func IsTaskDismissAlias(name string) bool {
	cfg := config.Get()
	if cfg == nil {
		return false
	}
	return cfg.Jira.TaskStatusMap.Done.MatchesAlias(name)
}

// IsSubtaskDismissAlias is the subtask analogue of IsTaskDismissAlias.
func IsSubtaskDismissAlias(name string) bool {
	cfg := config.Get()
	if cfg == nil {
		return false
	}
	return cfg.Jira.SubtaskStatusMap.Done.MatchesAlias(name)
}

// SubtaskDismissJiraName returns the Jira status name used when
// cascading dismissal onto sub-tasks (first alias of the done bucket).
func SubtaskDismissJiraName() string {
	cfg := config.Get()
	if cfg == nil {
		return ""
	}
	return cfg.Jira.SubtaskStatusMap.Done.FirstAlias()
}

// IssueInfo is the minimal snapshot the orchestrator works with.
type IssueInfo struct {
	Key               string
	Status            string
	AssigneeAccountID string
}
