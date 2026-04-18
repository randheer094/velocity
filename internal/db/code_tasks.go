package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/randheer094/velocity/internal/data"
)

func GetCodeTask(ctx context.Context, issueKey string) (*data.CodeTask, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	var t data.CodeTask
	var statusStr string
	err := p.QueryRow(ctx, `
		SELECT issue_key, parent_jira_key, repo_url, title, description,
		       branch, pr_url, status, error, last_error_stage, failed_at,
		       created_at, updated_at
		FROM code_tasks WHERE issue_key = $1
	`, issueKey).Scan(
		&t.IssueKey, &t.ParentJiraKey, &t.RepoURL, &t.Title, &t.Description,
		&t.Branch, &t.PRURL, &statusStr, &t.Error, &t.LastErrorStage, &t.FailedAt,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.Status = data.CodeStatus(statusStr)
	return &t, nil
}

func SaveCodeTask(ctx context.Context, t *data.CodeTask) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()
	_, err := p.Exec(ctx, `
		INSERT INTO code_tasks (
			issue_key, parent_jira_key, repo_url, title, description,
			branch, pr_url, status, error, last_error_stage, failed_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (issue_key) DO UPDATE SET
			parent_jira_key = EXCLUDED.parent_jira_key,
			repo_url = EXCLUDED.repo_url,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			branch = EXCLUDED.branch,
			pr_url = EXCLUDED.pr_url,
			status = EXCLUDED.status,
			error = EXCLUDED.error,
			last_error_stage = EXCLUDED.last_error_stage,
			failed_at = EXCLUDED.failed_at,
			updated_at = EXCLUDED.updated_at
	`,
		t.IssueKey, t.ParentJiraKey, t.RepoURL, t.Title, t.Description,
		t.Branch, t.PRURL, string(t.Status), t.Error, t.LastErrorStage, t.FailedAt,
		t.CreatedAt, t.UpdatedAt,
	)
	return err
}

// MarkCodeFailed upserts a minimal row if the task is absent.
func MarkCodeFailed(ctx context.Context, issueKey, parentKey, repoURL, title, branch, stage, errMsg string) error {
	t, _ := GetCodeTask(ctx, issueKey)
	now := time.Now().UTC()
	if t == nil {
		t = &data.CodeTask{
			IssueKey:      issueKey,
			ParentJiraKey: parentKey,
			RepoURL:       repoURL,
			Title:         title,
			Branch:        branch,
		}
	}
	t.Status = data.CodeFailed
	t.Error = errMsg
	t.LastErrorStage = stage
	t.FailedAt = &now
	return SaveCodeTask(ctx, t)
}

func MarkCodeDismissed(ctx context.Context, issueKey string) error {
	t, err := GetCodeTask(ctx, issueKey)
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	t.Status = data.CodeDismissed
	return SaveCodeTask(ctx, t)
}
