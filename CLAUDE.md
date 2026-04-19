# velocity

Single Go binary that runs a webhook-driven Jira → Claude → GitHub
agent. See [README.md](./README.md) for install/setup, and
[WORKFLOW.md](./WORKFLOW.md) for the ticket lifecycle.

## Where conventions live

| Topic | File |
| --- | --- |
| Scope, layout, naming, HTTP shape, agent boundaries | [.claude/rules/conventions.md](./.claude/rules/conventions.md) |
| Parent / sub-task lifecycle, wave math, failure/retry/dismiss | [WORKFLOW.md](./WORKFLOW.md) |

## Universal rules

1. **Webhooks are the only driver.** Two endpoints, no tickers, no polling.
2. **Two agents, one manager.** `arch` plans and manages waves; `code` executes one sub-task. `code` must not import `arch`.
3. **Shared libraries stay agent-agnostic.** No webhook handlers, no CLI imports, no cross-agent knowledge in `internal/{config,jira,github,git,llm,status,data,db}`.
4. **Jira issue keys are IDs.** DB rows, workspaces, git branches, PR titles all key off the issue key directly. Sub-task branch ≡ sub-task key.
5. **Embedded Postgres; workspaces ephemeral.** State under `~/.velocity/data/`; clones under `~/.velocity/workspace/<KEY>/`.
6. **Config unified; credentials in env vars.** One `config.json`; secrets (`JIRA_API_TOKEN`, `GH_TOKEN`, `JIRA_WEBHOOK_SECRET`, `GH_WEBHOOK_SECRET`) come from the environment. Setup is CLI-only.
7. **FIFO dispatch with parallel cap.** Handlers enqueue and return 202; workers drain the queue.
8. **Failures are first-class states.** `PLANNING FAILED`, `DEV FAILED` (`code_failed`), `DISMISSED` are real statuses. Retry = re-assign; dismiss is terminal.

## Source tree

```
cmd/velocity/main.go        binary entry — wires the cobra root
internal/
├── cli/                    cobra subcommands: setup, start/stop/restart/status/logs
├── server/                 http.Server + shutdown
├── webhook/                jira.go + github.go + queue.go (the only HTTP surface)
├── arch/                   plan + wave manager (parent rollup, dismiss cascade)
├── code/                   one sub-task (clone → Claude → commit → push → PR)
├── jira/                   REST client + shared singleton
├── github/                 REST client
├── git/                    clone / branch / commit / push
├── llm/                    Claude CLI provider
├── config/                 Config + paths + secret env var names
├── data/                   Plan / CodeTask value types
├── db/                     embedded Postgres + pgx pool + repositories
└── status/                 canonical → Jira status name helpers
```

## Editing this index

- Keep this file short. Promote growing sections into `.claude/rules/<topic>.md` or `WORKFLOW.md`.
- One rule, one home. Link, don't duplicate.
- Don't mirror values that live in code (ports, endpoint paths, status keys) — they rot.
