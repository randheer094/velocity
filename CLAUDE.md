# velocity

Single Go binary that runs a webhook-driven Jira → Claude → GitHub
agent. See [README.md](./README.md) for install/run, and
[WORKFLOW.md](./WORKFLOW.md) for the ticket lifecycle.

Contributor rules live in
[.claude/rules/conventions.md](./.claude/rules/conventions.md) and
[.claude/rules/maintenance.md](./.claude/rules/maintenance.md).
Lifecycle details (states, wave math, failure/retry/dismiss) live
in [WORKFLOW.md](./WORKFLOW.md). Don't duplicate them here.

## Universal rules

1. **Webhooks are the only driver.** Two endpoints, no tickers, no polling.
2. **Two agents, one manager.** `arch` plans and manages waves; `code` executes one sub-task. `code` must not import `arch`; `arch` must not import `code`.
3. **Shared libraries stay agent-agnostic.** No webhook handlers, no CLI imports, no cross-agent knowledge in `internal/{config,jira,github,git,llm,status,data,db}`.
4. **Jira issue keys are IDs.** DB rows, workspaces, git branches, PR titles all key off the issue key directly. Sub-task branch ≡ sub-task key.
5. **External Postgres; workspaces ephemeral.** Every DB connection field (`VELOCITY_DB_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_NAME`) comes from env; `sslmode` is hardcoded `disable`. Clones under `~/.velocity/workspace/<KEY>/`. Schema evolves via forward-only numbered migrations in `internal/db/migrations/` — never edit a shipped migration.
6. **Config is YAML; secrets are env vars.** One `config.yaml` (operator-written; see `config.example.yaml`) for Jira, LLM, and server tuning. Everything else (API tokens, webhook secrets, all Postgres fields) is env. No setup command, no runtime config reload.
7. **FIFO dispatch with parallel cap.** Handlers enqueue and return 202; workers drain the queue.
8. **Failures are first-class states.** `planning_failed`, `coding_failed` (operator-facing "Dev Failed") are real DB statuses. `Dismissed` collapses to canonical `done` via the configured alias; the raw Jira name lives in `jira_status`. Retry = re-assign; dismiss is terminal.

## Editing this index

- Keep this file short (~40 lines). Promote growing sections into `.claude/rules/<topic>.md` or `WORKFLOW.md`.
- One rule, one home. Link, don't duplicate.
- Don't mirror values that live in code (ports, endpoint paths, status keys, source tree) — they rot.
