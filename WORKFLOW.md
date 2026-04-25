# Workflow

End-to-end lifecycle of a velocity parent ticket and its sub-tasks,
from first assignment through parent DONE (or failure, or dismissal).

Velocity itself never ticks. Every step below runs in response to a
webhook. If you don't see a webhook arrow, nothing is happening.

## Roles

- **Architect** — Jira `accountId` assigned to parent tickets. Trigger
  for `arch.Run`.
- **Developer** — Jira `accountId` assigned to sub-tasks. Trigger for
  `code.Run`.
- The two IDs may be the same Jira user. Dispatch routes on issue
  type (parent vs sub-task), not on which ID is assigned.

## Status vocabulary

Velocity speaks in **canonical buckets** internally; operators map
each bucket to a Jira status name in `config.yaml`.

- Parent canonicals: `new`, `planning`, `planning_failed`, `coding`,
  `done`.
- Sub-task canonicals: `new`, `coding`, `coding_failed`, `in_review`,
  `done`.

Both maps support `aliases` per bucket. The conventional pattern is
to add `Dismissed` as an alias of `done` — that way "Dismissed" in
Jira collapses to canonical `done` while the raw Jira name is
preserved on the row's `jira_status` column. Cascade detection (a
parent dismissal cascades to sub-tasks) uses
`status.IsTaskDismissAlias` / `IsSubtaskDismissAlias`, which return
true only when the name matches an alias (not the default).

## Parent task lifecycle

```
       ┌─────── (re-assign) ────────┐
       │                            │
       ▼                            │
     NEW ──► PLANNING ──► CODING ──► DONE
                 │            │
                 ▼            │
         PLANNING_FAILED ─────┘   (re-assign wipes plan and replans)

  (any state) ──► DONE (via "Dismissed" alias)
       cascades dismissal to every still-open sub-task
```

DB row stores canonical `status = done` for both completion and
dismissal; `jira_status` distinguishes them by holding the actual
Jira status name (e.g. `"Done"` vs `"Dismissed"`).

### Transitions arch performs

| From | Event | To |
|---|---|---|
| NEW | parent assigned to architect | PLANNING |
| PLANNING | plan saved, sub-tasks created, wave-0 assigned | CODING |
| CODING | all waves complete | DONE |
| any arch stage | error | PLANNING_FAILED |

## Sub-task lifecycle

```
        ┌──────── (re-assign) ─────────┐
        │                              │
        ▼                              │
       NEW ──► CODING ──► IN_REVIEW ──► DONE
                  │            │
                  ▼            │
           CODING_FAILED ──────┘  (re-assign force-pushes same branch)

  (any state) ──► DONE (via "Dismissed" alias)
       counts as terminal for wave math
```

`coding_failed` is the canonical for the operator-facing "Dev
Failed" Jira status — velocity uses `coding_failed` internally.

### Transitions code performs

| From | Event | To |
|---|---|---|
| NEW | sub-task assigned to developer | CODING |
| CODING | Claude commits, branch pushed, PR opened | IN_REVIEW |
| IN_REVIEW | GitHub webhook reports `pull_request.merged=true` | DONE |
| any code stage | error | CODING_FAILED |

## Wave math

A plan groups sub-tasks into ordered waves. Tasks in the same wave
run in parallel (subject to `server.max_concurrency`, which gates
the LLM queue); waves are strictly sequential.

- Parent advances past wave *N* when every sub-task in wave *N* is
  canonical `done` (which includes the dismissed-alias case).
- A dismissed sub-task is terminal-success for wave math — the
  parent advances past it the same way a merged one does.
- `coding_failed` is **not** terminal. A failed sub-task blocks its
  wave until it is retried (re-assigned) or dismissed.
- When the last wave completes, arch transitions the parent to DONE
  and removes every workspace in the plan.

## Failure

Every stage of `arch.Run` and `code.Run` is labelled. On error, the
failure recorder:

1. Logs the full error to `~/.velocity/daemon.log`.
2. Posts a Jira comment (secrets redacted, ≤1000 chars) naming the
   stage.
3. Transitions the ticket to `PLANNING_FAILED` (arch) or
   `CODING_FAILED` (code) and records the configured Jira name on
   `jira_status`.
4. Writes `last_error`, `last_error_stage`, and `failed_at` on the
   DB row (`plans` or `code_tasks`).
5. Removes the workspace.

Panics in agent entry route through the same recorder with stage
`panic`.

## Retry

Retry is triggered by re-assignment, not by a new command.

1. Operator transitions the ticket back to a startable state
   (e.g. `NEW` for a parent, `NEW` for a sub-task).
2. Operator re-assigns to the same accountId.
3. Jira fires `jira:issue_updated`; the webhook enqueues the agent
   entry.
4. Agent guard inspects the DB row:
   - arch on `planning_failed` → `db.WipePlanChildren(parentKey)`,
     replan from scratch.
   - arch on `coding` → `AdvanceWave(parentKey)` (no replan).
   - code on `coding_failed` → re-clone, push with
     `--force-with-lease`, `CreateOrUpdatePR` refreshes title/body
     on the existing PR.
   - Terminal (`done` for parent or sub-task; `in_review` for
     sub-task) → no-op.

Branch name is always the sub-task key. Retries update the same
branch; they never open a second PR.

A daemon crash mid-job is recovered automatically on restart: the
row that was marked `running` is reset to `pending` and the agent
entry re-runs under its existing guard, so either the work
completes or the ticket lands in its failed state.

## Dismissal

Dismissal is terminal. It cancels any in-flight run via the per-
package cancel registry and wipes the workspace.

- **Dismissed parent**: cascades the configured dismiss alias to
  every still-open sub-task (best-effort; failures are logged, not
  retried), then writes `status = done` on the plan row with
  `jira_status` capturing the alias.
- **Dismissed sub-task**: writes `status = done` on the
  `code_tasks` row with `jira_status` set to the dismiss alias.
  The parent's wave advances past it like a merged sub-task.

## PR iteration

Two GitHub events can trigger in-place updates on an open PR. The
PR does **not** need to have been opened by velocity — any PR in a
repo whose webhook points at velocity can be iterated on.

- **CI failure** — `workflow_run` with `conclusion=failure` and a
  non-empty `pull_requests` array (the PR-only gate; runs from
  pushes to the default branch are ignored). Velocity pre-fetches
  a trimmed tail of each failed job's log via the Actions API and
  inlines it into the prompt so the LLM has the failure context
  without needing network access from its sandbox. The commit
  subject is derived from the first error line in the log
  (`<branch>: fix CI: <error>`).
- **`/velocity <instruction>` comment** — `issue_comment` with
  `action=created` on a PR. Empty instructions get a usage reply on
  the PR. The comment prefix `/velocity` is required; anything else
  is ignored.

Both triggers enqueue `code.Iterate` with the repo clone URL, head
branch, and PR URL carried from the webhook payload. Iterate then:

1. Removes any prior workspace and fresh-clones the repo.
2. Checks out the PR branch.
3. Runs the code LLM, which is prompted to **first rebase onto the
   default branch, resolving any conflicts**, then apply the
   follow-up request (CI failure summary or user instruction).
4. Commits any remaining uncommitted edits and force-pushes the
   branch with lease.

If a velocity `code_tasks` row exists for the branch, its title and
description seed the prompt for additional context; the row is not
required.

Iterate failures are logged, posted to the PR as a comment, and —
when the branch is a valid Jira issue key — also posted to the Jira
issue. Velocity never transitions a PR's status on iterate failure.
Operators retry by posting another `/velocity` comment or by
re-running the failing CI check.

## Dispatch

The HTTP handlers do zero work. Each webhook:

```
handler ──► webhook.Enqueue(kind, name, payload)
               │
               ▼
          INSERT INTO webhook_jobs (queue, status='pending')
               │            queue ∈ {llm, ops}
               ▼
   ┌─ llm pool: N pollers (server.max_concurrency)  ─┐
   │  claim WHERE queue='llm' FOR UPDATE SKIP LOCKED │
   └─ ops pool: 1 poller (strictly serialized)       ─┘
                  claim WHERE queue='ops' FOR UPDATE SKIP LOCKED
                  │
                  ▼
        dispatch(kind) → arch.Run / code.Run / code.Iterate (llm)
                       │ arch.AdvanceWave / arch.AssignWave /
                       │ arch.Archive / arch.OnDismissed /
                       │ code.MarkMerged / code.OnDismissed /
                       │ code.Cleanup (ops)
```

- Handlers return `202` immediately.
- Each row represents **one logical step**. Handlers never inline-call
  another kind's logic — when more work is needed they enqueue a
  follow-up row and return.
- The **llm queue** carries long-running `arch.Run` / `code.Run` /
  `code.Iterate`. Parallelism = `server.max_concurrency` (default 1
  = strict serial; raise it if your Claude/Jira/GitHub stacks
  tolerate it).
- The **ops queue** carries every other kind: lightweight DB/Jira/
  GitHub steps and workspace cleanup. It runs strictly serialized
  (1 worker), so `arch.AdvanceWave`, `arch.AssignWave`,
  `arch.Archive`, `arch.OnDismissed`, `code.MarkMerged`,
  `code.OnDismissed`, and `code.Cleanup` never race each other.
- Cross-queue safety: `code.Cleanup` and `arch.Archive` consult the
  per-package `IsInFlight` map and skip the workspace `RemoveAll`
  when the LLM-side `Run` for that key is still active. The next
  `Run`'s pre-clone `RemoveAll` handles eventual cleanup.
- `server.queue_size` is a **soft cap** on pending rows, applied
  per queue (a flooded ops backlog can't starve LLM enqueues).
- Stale enqueues are safe: each agent entry re-reads the DB row and
  no-ops if the ticket is already terminal or in a compatible phase.
- On daemon start, any row stuck in `running` (the previous process
  died mid-job) is reset to `pending` so it runs again. A detached
  context marks each row `done`/`failed` even if the shutdown ctx
  has fired.
- Completed rows stay in `webhook_jobs` as history:
  `SELECT queue, status, count(*) FROM webhook_jobs GROUP BY 1, 2;`
  gives a live view of both queues.

## Call-chain summary

| Trigger | Path | Queue |
|---|---|---|
| Parent assigned to architect | `POST /webhook/jira` → `webhook.Enqueue` → `arch.Run` → enqueues `arch.AssignWave(0)` | llm → ops |
| Sub-task assigned to developer | `POST /webhook/jira` → `webhook.Enqueue` → `code.Run` | llm |
| Sub-task transitions to DONE | `POST /webhook/jira` → `webhook.Enqueue` → `arch.AdvanceWave` → enqueues `arch.AssignWave(N)` or `arch.Archive` | ops |
| Sub-task transitions to DONE via "Dismissed" alias | `POST /webhook/jira` → `webhook.Enqueue` → `code.OnDismissed` → enqueues `code.Cleanup` + `arch.AdvanceWave` | ops |
| Parent transitions to DONE via "Dismissed" alias | `POST /webhook/jira` → `webhook.Enqueue` → `arch.OnDismissed` | ops |
| PR merged | `POST /webhook/github` → `webhook.Enqueue` → `code.MarkMerged` → enqueues `code.Cleanup` | ops |
| CI failed on any PR | `POST /webhook/github` (`workflow_run` with `pull_requests`) → `webhook.Enqueue` → `code.Iterate` | llm |
| `/velocity` comment on any PR | `POST /webhook/github` (`issue_comment`) → `webhook.Enqueue` → `code.Iterate` | llm |

All cross-component communication — including arch→code — goes via
Jira (assignment, transition) or GitHub (PR events). Never via
shared in-memory state.
