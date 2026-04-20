package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/randheer094/velocity/internal/data"
)

func GetPlan(ctx context.Context, parentKey string) (*data.Plan, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}

	var plan data.Plan
	var statusStr string
	err := p.QueryRow(ctx, `
		SELECT parent_jira_key, name, repo_url, active_wave_idx,
		       status, jira_status, last_error, last_error_stage, failed_at,
		       completed_at, created_at, updated_at
		FROM plans WHERE parent_jira_key = $1
	`, parentKey).Scan(
		&plan.ParentJiraKey, &plan.Name, &plan.RepoURL, &plan.ActiveWaveIdx,
		&statusStr, &plan.JiraStatus, &plan.LastError, &plan.LastErrorStage, &plan.FailedAt,
		&plan.CompletedAt, &plan.CreatedAt, &plan.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	plan.Status = data.PlanStatus(statusStr)

	waves, err := loadPlanWaves(ctx, p, parentKey)
	if err != nil {
		return nil, err
	}
	plan.Waves = waves

	return &plan, nil
}

// SavePlan upserts the plans row and rewrites plan_waves from the in-memory Plan.
func SavePlan(ctx context.Context, plan *data.Plan) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	plan.UpdatedAt = time.Now().UTC()
	if plan.Status == "" {
		plan.Status = data.PlanCoding
	}

	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO plans (parent_jira_key, name, repo_url, active_wave_idx,
		                   status, jira_status, last_error, last_error_stage, failed_at,
		                   completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (parent_jira_key) DO UPDATE SET
			name = EXCLUDED.name,
			repo_url = EXCLUDED.repo_url,
			active_wave_idx = EXCLUDED.active_wave_idx,
			status = EXCLUDED.status,
			jira_status = EXCLUDED.jira_status,
			last_error = EXCLUDED.last_error,
			last_error_stage = EXCLUDED.last_error_stage,
			failed_at = EXCLUDED.failed_at,
			completed_at = EXCLUDED.completed_at,
			updated_at = EXCLUDED.updated_at
	`, plan.ParentJiraKey, plan.Name, plan.RepoURL, plan.ActiveWaveIdx,
		string(plan.Status), plan.JiraStatus, plan.LastError, plan.LastErrorStage, plan.FailedAt,
		plan.CompletedAt, plan.CreatedAt, plan.UpdatedAt); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM plan_waves WHERE parent_jira_key = $1`, plan.ParentJiraKey); err != nil {
		return err
	}
	for waveIdx, w := range plan.Waves {
		for pos, t := range w.Tasks {
			if _, err := tx.Exec(ctx, `
				INSERT INTO plan_waves (parent_jira_key, wave_idx, position, title, description, jira_key)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, plan.ParentJiraKey, waveIdx, pos, t.Title, t.Description, t.JiraKey); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

// MarkPlanDone flips status to done; rows stay for history.
func MarkPlanDone(ctx context.Context, parentKey, jiraStatus string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, jira_status = $3, completed_at = $4, updated_at = $4
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanDone), jiraStatus, now)
	return err
}

func MarkPlanFailed(ctx context.Context, parentKey, jiraStatus, stage, errMsg string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, jira_status = $3, last_error = $4, last_error_stage = $5,
		    failed_at = $6, updated_at = $6
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanPlanningFailed), jiraStatus, errMsg, stage, now)
	return err
}

func MarkPlanDismissed(ctx context.Context, parentKey, jiraStatus string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, jira_status = $3, updated_at = $4
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanDone), jiraStatus, now)
	return err
}

// WipePlanChildren clears waves for a retry; the plans row stays (SavePlan overwrites it).
func WipePlanChildren(ctx context.Context, parentKey string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	_, err := p.Exec(ctx, `DELETE FROM plan_waves WHERE parent_jira_key = $1`, parentKey)
	return err
}

// TaskPredecessors returns jira_keys of every task in the wave immediately
// preceding the wave that contains jiraKey. Empty if jiraKey is in wave 0
// or not part of any plan.
func TaskPredecessors(ctx context.Context, jiraKey string) ([]string, error) {
	return neighboringWaveKeys(ctx, jiraKey, -1)
}

// TaskSuccessors returns jira_keys of every task in the wave immediately
// following the wave that contains jiraKey. Empty if jiraKey is in the
// last wave or not part of any plan.
func TaskSuccessors(ctx context.Context, jiraKey string) ([]string, error) {
	return neighboringWaveKeys(ctx, jiraKey, +1)
}

func neighboringWaveKeys(ctx context.Context, jiraKey string, delta int) ([]string, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	rows, err := p.Query(ctx, `
		SELECT neighbor.jira_key
		FROM plan_waves self
		JOIN plan_waves neighbor
		  ON neighbor.parent_jira_key = self.parent_jira_key
		 AND neighbor.wave_idx = self.wave_idx + $2
		WHERE self.jira_key = $1
	`, jiraKey, delta)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

func scanStringColumn(rows pgx.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func loadPlanWaves(ctx context.Context, p *pgxpool.Pool, parentKey string) ([]data.Wave, error) {
	rows, err := p.Query(ctx, `
		SELECT wave_idx, title, description, jira_key
		FROM plan_waves
		WHERE parent_jira_key = $1
		ORDER BY wave_idx ASC, position ASC
	`, parentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var waves []data.Wave
	for rows.Next() {
		var waveIdx int
		var t data.PlannedTask
		if err := rows.Scan(&waveIdx, &t.Title, &t.Description, &t.JiraKey); err != nil {
			return nil, err
		}
		for len(waves) <= waveIdx {
			waves = append(waves, data.Wave{})
		}
		waves[waveIdx].Tasks = append(waves[waveIdx].Tasks, t)
	}
	return waves, rows.Err()
}
