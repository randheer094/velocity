# Conventions

Rules for the velocity Go binary. Lifecycle details (states, wave
math, failure/retry/dismiss) live in [WORKFLOW.md](../../WORKFLOW.md)
— don't duplicate them here. Agent-specific invariants (arch prompt
schema, code PR body, branch naming) live beside the agent code.

**Naming.** Internal symbols use **arch** and **code**. "Planner"
and "coder" are not used in code, types, config keys, comments, or
prompts. "Dev" only appears in operator-configured Jira status
names (e.g. `DEV FAILED`) — never as an internal symbol.

## Scope

- One Go binary, two HTTP endpoints (`POST /webhook/jira`,
  `POST /webhook/github`), plus `GET /healthz`. Setup is CLI-only
  via `velocity setup`.
- Do not re-introduce: schedulers, tickers, polling loops,
  filesystem queues, manual task-create endpoints, a web UI, per-
  agent binaries, subprocess workers, or HTTP setup surfaces. The
  webhook-only collapse took these out deliberately.
- New features belong in `internal/arch/`, `internal/code/`, or a
  shared library package — never in a new binary or a parallel loop.

## Layout

- `cmd/velocity/main.go` is tiny: `cli.NewRootCmd().Execute()`.
- Agent code is flat: `internal/arch/` and `internal/code/`, no
  intermediate `agents/` directory.
- `net/http` is imported only by `internal/server/` and
  `internal/webhook/`.
- `internal/cli/` imports `internal/server/` and `internal/config/`
  but never an agent package.
- Shared libraries (`config`, `jira`, `github`, `git`, `llm`,
  `status`, `data`, `db`) have no webhook handlers, no CLI imports,
  no cross-imports to `arch` or `code`.

## Naming

- Binary: `velocity`. Module: `github.com/randheer094/velocity`.
- Data dir: `~/.velocity` (override `--dir`). Only two subdirs:
  `data/` (Postgres cluster) and `workspace/` (per-ticket clones).
- IDs are Jira issue keys. Plans key on parent key; `code_tasks`
  rows key on sub-task key; workspaces named `workspace/<KEY>/`.
  Git branches **must** equal the sub-task key exactly.

## HTTP shape

- Mux in `server.Run` has exactly three routes; new ones go under
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
- `server.Run` order: `config.EnsureRuntimeDirs()` →
  `jira.Reinit()` → `db.Start()` → `webhook.Start()` →
  `ListenAndServe`. Construct `jira.Client` only via `Reinit`
  (runtime) or `NewWithCreds` (setup form).
- Shutdown (SIGINT/SIGTERM): `http.Server.Shutdown` → `webhook.Drain`
  → `db.Stop`, all inside a 10 s budget.
- No in-process setup endpoint, no runtime config reload. Rotate
  config via stop → `velocity setup --edit` → start. Rotate
  secrets by re-exporting env vars and restarting.

## Shared library packages

- **`internal/llm/`** — Claude CLI wrapper (`claude --print
  --output-format text`). Per-role options from `LLMRoleConfig`.
- **`internal/config/`** — config loader + paths + secret env var
  names (`JIRA_API_TOKEN`, `GH_TOKEN`, `JIRA_WEBHOOK_SECRET`,
  `GH_WEBHOOK_SECRET`). Setup is the only writer of `config.json`.
- **`internal/jira/`** — REST client + shared singleton. No
  orchestration.
- **`internal/github/`** — REST client. HMAC verification lives in
  `internal/webhook/signature.go` (shared by both handlers).
- **`internal/git/`** — `git` CLI wrappers. Always configure the
  authenticated remote before `Push` / `PushForceWithLease`.
- **`internal/status/`** — canonical ↔ Jira name resolver. Only
  this package speaks in Jira status-name strings; everyone else
  speaks in canonicals.
- **`internal/data/`** — `Plan` / `CodeTask` value types, keyed on
  Jira issue keys. No I/O.
- **`internal/db/`** — embedded Postgres + repositories. Schema is
  `CREATE TABLE IF NOT EXISTS` only — no ALTER, no migrations. Wipe
  `~/.velocity/data/` to pick up schema changes.

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

## Code style

- Default to **no comments**. Only add one when the WHY is
  non-obvious (hidden constraint, subtle invariant, bug workaround).
- Don't explain WHAT — well-named identifiers do that. Don't
  reference callers, tasks, or fix history; that's what PRs are for.
- Keep doc comments to a single line where possible.
- `CLAUDE.md` ≤ ~60 lines; this file ≤ ~120 lines. Promote growing
  sections into their own topic file.

## What does NOT belong here

- Pollers, tickers, cron tasks, goroutine-based loops that scan
  filesystem queues.
- A web UI, dashboard, HTTP setup surface, or manual task-create
  endpoint.
- New shared `internal/` packages beyond those listed above.
- Per-agent config files, or on-disk secret stores — secrets come from env vars only.
- `cmd/<other>/` binaries.
