# Conventions

Rules for the velocity Go binary. Lifecycle details (states, wave
math, failure/retry/dismiss) live in [WORKFLOW.md](../../WORKFLOW.md)
— don't duplicate them here. Agent-specific invariants (arch prompt
schema, code PR body, branch naming) live beside the agent code.

## Naming

- Internal symbols use **arch** and **code**. "Planner" and "coder"
  are not used in code, types, config keys, comments, or prompts.
  "Dev" only appears in operator-configured Jira status names
  (e.g. `DEV FAILED`) — never as an internal symbol.
- Binary: `velocity`. Module: `github.com/randheer094/velocity`.
- Data dir: `~/.velocity` (override `--dir`). Holds `workspace/`
  (per-ticket clones) and the daemon pid/log. Postgres is external.
- IDs are Jira issue keys. Plans key on parent key; `code_tasks`
  rows key on sub-task key; workspaces named `workspace/<KEY>/`.
  Git branches **must** equal the sub-task key exactly.

## Scope (do not re-introduce)

- Schedulers, tickers, polling loops, filesystem queues.
- Manual task-create endpoints, a web UI, or any HTTP setup surface.
- Per-agent binaries, subprocess workers, `cmd/<other>/` binaries.
- New shared `internal/` packages beyond the existing set.
- Per-agent config files or on-disk secret stores.

New features belong in `internal/arch/`, `internal/code/`, or an
existing shared library package — never in a new binary or a
parallel loop.

## HTTP shape

- `server.Run`'s mux has exactly three routes; new ones go under
  `/webhook/` or not at all.
- Jira handler dispatches on **issue type first**
  (`issuetype.subtask` or presence of `parent`). Sub-task →
  `code.Run`; parent → `arch.Run`. Assignee `accountId` is a gate,
  not the router — the same Jira user may serve both roles.
- Handlers do no work directly: build a `webhook.Job`, call
  `webhook.Enqueue(...)`, return `202`.
- Idempotency layers: the FIFO queue preserves arrival order; each
  agent entry re-reads DB status to no-op stale enqueues.

## Lifespan

- `velocity start` self-spawns with `VELOCITY_DAEMON_CHILD=1`. The
  parent writes `~/.velocity/daemon.pid` and exits.
- Construct `jira.Client` only via `jira.Reinit` (runtime) or
  `jira.NewWithCreds` (tests).
- No in-process setup endpoint, no runtime config reload. Rotate
  config via stop → edit `config.yaml` → start. Rotate secrets by
  re-exporting env vars and restarting.

## Agent boundaries

`arch` is the wave manager; `code` owns one sub-task end-to-end.

- ✅ `webhook` may import `arch` and `code`.
- ✅ `arch` and `code` may import shared libraries.
- ❌ `code` must not import `arch`. Wave advancement is
  `webhook → arch.AdvanceWave` on sub-task DONE.
- ❌ `arch` must not import `code`. Wave start is a Jira
  assignment — the Jira webhook then invokes `code.Run`.

Cross-component communication beyond library calls goes via Jira
(assignment, transition) or GitHub (PR events). Never shared
in-memory state except `jira.Shared()`.

## Database

- Schema evolves via forward-only numbered migrations embedded from
  `internal/db/migrations/NNNN_*.sql`; each runs in its own
  transaction and is recorded in `schema_migrations`. Never edit a
  shipped migration — add a new one.
- `internal/status/` is the only package that speaks in Jira
  status-name strings; everyone else speaks in canonicals.

## Code style

- Default to **no comments**. Only add one when the WHY is
  non-obvious (hidden constraint, subtle invariant, bug workaround).
- Don't explain WHAT — well-named identifiers do that. Don't
  reference callers, tasks, or fix history; that's what PRs are for.
- Keep doc comments to a single line where possible.
- `CLAUDE.md` ≤ ~40 lines; this file ≤ ~90 lines. Promote growing
  sections into their own topic file.
