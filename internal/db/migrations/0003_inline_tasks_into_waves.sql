ALTER TABLE plan_waves
    ADD COLUMN IF NOT EXISTS title       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

UPDATE plan_waves pw
SET title = pt.title,
    description = pt.description
FROM plan_tasks pt
WHERE pw.parent_jira_key = pt.parent_jira_key
  AND pw.task_id         = pt.task_id;

ALTER TABLE plan_waves DROP COLUMN IF EXISTS task_id;

DROP INDEX IF EXISTS plan_tasks_jira_key_idx;
DROP TABLE IF EXISTS plan_task_deps;
DROP TABLE IF EXISTS plan_tasks;

CREATE INDEX IF NOT EXISTS plan_waves_jira_key_idx ON plan_waves (jira_key);
