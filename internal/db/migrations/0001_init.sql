CREATE TABLE IF NOT EXISTS plans (
    parent_jira_key    TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    repo_url           TEXT NOT NULL,
    active_wave_idx    INT  NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'active',
    last_error         TEXT NOT NULL DEFAULT '',
    last_error_stage   TEXT NOT NULL DEFAULT '',
    failed_at          TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS plan_tasks (
    parent_jira_key   TEXT NOT NULL REFERENCES plans(parent_jira_key) ON DELETE CASCADE,
    task_id           TEXT NOT NULL,
    position          INT  NOT NULL,
    title             TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    jira_key          TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (parent_jira_key, task_id)
);
CREATE INDEX IF NOT EXISTS plan_tasks_jira_key_idx ON plan_tasks (jira_key);

CREATE TABLE IF NOT EXISTS plan_waves (
    parent_jira_key   TEXT NOT NULL REFERENCES plans(parent_jira_key) ON DELETE CASCADE,
    wave_idx          INT  NOT NULL,
    position          INT  NOT NULL,
    task_id           TEXT NOT NULL,
    jira_key          TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (parent_jira_key, wave_idx, position)
);

CREATE TABLE IF NOT EXISTS plan_task_deps (
    parent_jira_key     TEXT NOT NULL REFERENCES plans(parent_jira_key) ON DELETE CASCADE,
    task_id             TEXT NOT NULL,
    depends_on_task_id  TEXT NOT NULL,
    PRIMARY KEY (parent_jira_key, task_id, depends_on_task_id)
);
CREATE INDEX IF NOT EXISTS plan_task_deps_depends_idx
    ON plan_task_deps (parent_jira_key, depends_on_task_id);

CREATE TABLE IF NOT EXISTS code_tasks (
    issue_key          TEXT PRIMARY KEY,
    parent_jira_key    TEXT NOT NULL,
    repo_url           TEXT NOT NULL,
    title              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    branch             TEXT NOT NULL DEFAULT '',
    pr_url             TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL,
    error              TEXT NOT NULL DEFAULT '',
    last_error_stage   TEXT NOT NULL DEFAULT '',
    failed_at          TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
