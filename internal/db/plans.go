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
		       status, last_error, last_error_stage, failed_at,
		       completed_at, created_at, updated_at
		FROM plans WHERE parent_jira_key = $1
	`, parentKey).Scan(
		&plan.ParentJiraKey, &plan.Name, &plan.RepoURL, &plan.ActiveWaveIdx,
		&statusStr, &plan.LastError, &plan.LastErrorStage, &plan.FailedAt,
		&plan.CompletedAt, &plan.CreatedAt, &plan.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	plan.Status = data.PlanStatus(statusStr)

	tasks, err := loadPlanTasks(ctx, p, parentKey)
	if err != nil {
		return nil, err
	}
	plan.TaskList = tasks

	waves, err := loadPlanWaves(ctx, p, parentKey)
	if err != nil {
		return nil, err
	}
	plan.Waves = waves

	return &plan, nil
}

// SavePlan rebuilds plan_task_deps from the wave structure: task in wave N depends on every task in wave N-1.
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
		plan.Status = data.PlanActive
	}

	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO plans (parent_jira_key, name, repo_url, active_wave_idx,
		                   status, last_error, last_error_stage, failed_at,
		                   completed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (parent_jira_key) DO UPDATE SET
			name = EXCLUDED.name,
			repo_url = EXCLUDED.repo_url,
			active_wave_idx = EXCLUDED.active_wave_idx,
			status = EXCLUDED.status,
			last_error = EXCLUDED.last_error,
			last_error_stage = EXCLUDED.last_error_stage,
			failed_at = EXCLUDED.failed_at,
			completed_at = EXCLUDED.completed_at,
			updated_at = EXCLUDED.updated_at
	`, plan.ParentJiraKey, plan.Name, plan.RepoURL, plan.ActiveWaveIdx,
		string(plan.Status), plan.LastError, plan.LastErrorStage, plan.FailedAt,
		plan.CompletedAt, plan.CreatedAt, plan.UpdatedAt); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM plan_tasks WHERE parent_jira_key = $1`, plan.ParentJiraKey); err != nil {
		return err
	}
	for i, t := range plan.TaskList {
		if _, err := tx.Exec(ctx, `
			INSERT INTO plan_tasks (parent_jira_key, task_id, position, title, description, jira_key)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, plan.ParentJiraKey, t.ID, i, t.Title, t.Description, t.JiraKey); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM plan_waves WHERE parent_jira_key = $1`, plan.ParentJiraKey); err != nil {
		return err
	}
	for waveIdx, w := range plan.Waves {
		for pos, ref := range w.Tasks {
			if _, err := tx.Exec(ctx, `
				INSERT INTO plan_waves (parent_jira_key, wave_idx, position, task_id, jira_key)
				VALUES ($1, $2, $3, $4, $5)
			`, plan.ParentJiraKey, waveIdx, pos, ref.ID, ref.JiraKey); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM plan_task_deps WHERE parent_jira_key = $1`, plan.ParentJiraKey); err != nil {
		return err
	}
	for waveIdx := 1; waveIdx < len(plan.Waves); waveIdx++ {
		prev := plan.Waves[waveIdx-1].Tasks
		for _, ref := range plan.Waves[waveIdx].Tasks {
			for _, dep := range prev {
				if _, err := tx.Exec(ctx, `
					INSERT INTO plan_task_deps (parent_jira_key, task_id, depends_on_task_id)
					VALUES ($1, $2, $3)
					ON CONFLICT DO NOTHING
				`, plan.ParentJiraKey, ref.ID, dep.ID); err != nil {
					return err
				}
			}
		}
	}

	return tx.Commit(ctx)
}

// MarkPlanDone flips status to done; rows stay for history.
func MarkPlanDone(ctx context.Context, parentKey string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, completed_at = $3, updated_at = $3
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanDone), now)
	return err
}

func MarkPlanFailed(ctx context.Context, parentKey, stage, errMsg string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, last_error = $3, last_error_stage = $4,
		    failed_at = $5, updated_at = $5
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanPlanningFailed), errMsg, stage, now)
	return err
}

func MarkPlanDismissed(ctx context.Context, parentKey string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	now := time.Now().UTC()
	_, err := p.Exec(ctx, `
		UPDATE plans
		SET status = $2, updated_at = $3
		WHERE parent_jira_key = $1
	`, parentKey, string(data.PlanDismissed), now)
	return err
}

// WipePlanChildren clears tasks/waves/deps for a retry; the plans row stays (SavePlan overwrites it).
func WipePlanChildren(ctx context.Context, parentKey string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{
		`DELETE FROM plan_task_deps WHERE parent_jira_key = $1`,
		`DELETE FROM plan_waves WHERE parent_jira_key = $1`,
		`DELETE FROM plan_tasks WHERE parent_jira_key = $1`,
	} {
		if _, err := tx.Exec(ctx, q, parentKey); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func TaskPredecessors(ctx context.Context, jiraKey string) ([]string, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	rows, err := p.Query(ctx, `
		SELECT dep.jira_key
		FROM plan_task_deps d
		JOIN plan_tasks self
		  ON self.parent_jira_key = d.parent_jira_key
		 AND self.task_id = d.task_id
		JOIN plan_tasks dep
		  ON dep.parent_jira_key = d.parent_jira_key
		 AND dep.task_id = d.depends_on_task_id
		WHERE self.jira_key = $1
	`, jiraKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStringColumn(rows)
}

func TaskSuccessors(ctx context.Context, jiraKey string) ([]string, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	rows, err := p.Query(ctx, `
		SELECT succ.jira_key
		FROM plan_task_deps d
		JOIN plan_tasks self
		  ON self.parent_jira_key = d.parent_jira_key
		 AND self.task_id = d.depends_on_task_id
		JOIN plan_tasks succ
		  ON succ.parent_jira_key = d.parent_jira_key
		 AND succ.task_id = d.task_id
		WHERE self.jira_key = $1
	`, jiraKey)
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

func loadPlanTasks(ctx context.Context, p *pgxpool.Pool, parentKey string) ([]data.PlannedTask, error) {
	rows, err := p.Query(ctx, `
		SELECT task_id, title, description, jira_key
		FROM plan_tasks
		WHERE parent_jira_key = $1
		ORDER BY position ASC
	`, parentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []data.PlannedTask
	for rows.Next() {
		var t data.PlannedTask
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.JiraKey); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func loadPlanWaves(ctx context.Context, p *pgxpool.Pool, parentKey string) ([]data.Wave, error) {
	rows, err := p.Query(ctx, `
		SELECT wave_idx, task_id, jira_key
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
		var ref data.WaveRef
		if err := rows.Scan(&waveIdx, &ref.ID, &ref.JiraKey); err != nil {
			return nil, err
		}
		for len(waves) <= waveIdx {
			waves = append(waves, data.Wave{})
		}
		waves[waveIdx].Tasks = append(waves[waveIdx].Tasks, ref)
	}
	return waves, rows.Err()
}
