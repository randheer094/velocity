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

## Parent task lifecycle

```
           ┌───────────── (re-assign) ─────────────┐
           │                                       │
           ▼                                       │
         NEW ──► PLANNING ──► SUBTASK_IN_PROGRESS ──► DONE
                     │                    │
                     ▼                    │
            PLANNING_FAILED ──────────────┘  (re-assign wipes plan and replans)

  (any state) ──► DISMISSED  (cascades to every open sub-task)
```

Canonical buckets: `new`, `planning`, `planning_failed`,
`subtask_in_progress`, `done`, `dismissed`. Operators map each bucket
to a Jira status name in `config.yaml` (`jira.task_status_map`).

### Transitions arch performs

| From | Event | To |
|---|---|---|
| NEW | parent assigned to architect | PLANNING |
| PLANNING | plan saved, sub-tasks created, wave-0 assigned | SUBTASK_IN_PROGRESS |
| SUBTASK_IN_PROGRESS | all waves complete | DONE |
| any arch stage | error | PLANNING_FAILED |

## Sub-task lifecycle

```
           ┌──────── (re-assign) ────────┐
           │                             │
           ▼                             │
  READY_FOR_DEV ──► IN_PROGRESS ──► PR_OPEN ──► DONE
                          │            │
                          ▼            │
                      DEV_FAILED ──────┘  (re-assign force-pushes same branch)

  (any state) ──► DISMISSED  (counts as terminal for wave math)
```

Canonical buckets: `new`, `in_progress`, `pr_open`, `code_failed`,
`done`, `dismissed`. Operators map them in
`jira.subtask_status_map`. "Dev Failed" is the conventional Jira
name for `code_failed`; velocity uses `code_failed` internally.

### Transitions code performs

| From | Event | To |
|---|---|---|
| READY_FOR_DEV | sub-task assigned to developer | IN_PROGRESS |
| IN_PROGRESS | Claude commits, branch pushed, PR opened | PR_OPEN |
| PR_OPEN | GitHub webhook reports `pull_request.merged=true` | DONE |
| any code stage | error | DEV_FAILED |

## Wave math

A plan groups sub-tasks into ordered waves. Tasks in the same wave
run in parallel (subject to `server.max_concurrency`); waves are
strictly sequential.

- Parent advances past wave *N* when every sub-task in wave *N* is in
  `done` **or** `dismissed`.
- A dismissed sub-task is terminal-success for wave math — the parent
  advances past it the same way a merged one does.
- `code_failed` is **not** terminal. A failed sub-task blocks its
  wave until it is retried (re-assigned) or dismissed.
- When the last wave completes, arch transitions the parent to DONE
  and removes every workspace in the plan.

## Failure

Every stage of `arch.Run` and `code.Run` is labelled. On error, the
failure recorder:

1. Logs the full error to `~/.velocity/daemon.log`.
2. Posts a Jira comment (secrets redacted, ≤1000 chars) naming the
   stage.
3. Transitions the ticket to `PLANNING_FAILED` (arch) or `DEV_FAILED`
   (code).
4. Writes `last_error`, `last_error_stage`, and `failed_at` on the
   DB row (`plans` or `code_tasks`).
5. Removes the workspace.

Panics in agent entry route through the same recorder with stage
`panic`.

## Retry

Retry is triggered by re-assignment, not by a new command.

1. Operator transitions the ticket back to a startable state (e.g.
   `NEW` for a parent, `READY_FOR_DEV` for a sub-task).
2. Operator re-assigns to the same accountId.
3. Jira fires `jira:issue_updated`; the webhook enqueues the agent
   entry.
4. Agent guard inspects the DB row:
   - arch on `planning_failed` → `db.WipePlanChildren(parentKey)`,
     replan from scratch.
   - arch on `active` → `AdvanceWave(parentKey)` (no replan).
   - code on `code_failed` → re-clone, push with
     `--force-with-lease`, `CreateOrUpdatePR` refreshes title/body on
     the existing PR.
   - Terminal (`done`, `dismissed`, or `pr_open` for code) → no-op.

Branch name is always the sub-task key. Retries update the same
branch; they never open a second PR.

## Dismissal

Dismissal is terminal. It cancels any in-flight run via the per-
package cancel registry and wipes the workspace.

- **Dismissed parent**: cascades DISMISSED to every still-open
  sub-task (best-effort; failures are logged, not retried), then
  marks the plan row dismissed.
- **Dismissed sub-task**: marks the `code_tasks` row dismissed. The
  parent's wave advances past it like a merged sub-task.

## Dispatch

The HTTP handlers do zero work. Each webhook:

```
handler ──► webhook.Enqueue(Job{Name, Fn})
               │
               ▼
          in-memory FIFO queue (bounded by server.queue_size)
               │
               ▼
          N workers (server.max_concurrency, default 1 = serial)
               │
               ▼
        arch.Run / code.Run / arch.AdvanceWave / code.MarkMerged
```

- Handlers return `202` immediately.
- With `max_concurrency = 1`, jobs are strictly serial in arrival
  order.
- With `N > 1`, dequeue order is still FIFO; up to N jobs run in
  parallel.
- Full queue → job dropped and logged (backpressure).
- Stale enqueues are safe: each agent entry re-reads the DB row and
  no-ops if the ticket is already terminal, dismissed, or in a
  compatible phase.

## Call-chain summary

| Trigger | Path |
|---|---|
| Parent assigned to architect | `POST /webhook/jira` → `webhook.Enqueue` → `arch.Run` |
| Sub-task assigned to developer | `POST /webhook/jira` → `webhook.Enqueue` → `code.Run` |
| Sub-task transitions to DONE | `POST /webhook/jira` → `webhook.Enqueue` → `arch.AdvanceWave` |
| Sub-task transitions to DISMISSED | `POST /webhook/jira` → `webhook.Enqueue` → `code.OnDismissed` + `arch.AdvanceWave` |
| Parent transitions to DISMISSED | `POST /webhook/jira` → `webhook.Enqueue` → `arch.OnDismissed` |
| PR merged | `POST /webhook/github` → `webhook.Enqueue` → `code.MarkMerged` |

All cross-component communication — including arch→code — goes via
Jira (assignment, transition) or GitHub (PR events). Never via
shared in-memory state.
