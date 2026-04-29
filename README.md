# velocity

[![CI](https://img.shields.io/github/actions/workflow/status/randheer094/velocity/ci.yml?branch=main&label=CI)](https://github.com/randheer094/velocity/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/randheer094/velocity?label=release&display_name=tag)](https://github.com/randheer094/velocity/releases/latest)
[![Coverage](https://raw.githubusercontent.com/randheer094/velocity/badges/coverage.svg)](https://github.com/randheer094/velocity/actions/workflows/ci.yml)

Webhook-driven agent that takes a Jira parent ticket assigned to an
architect, plans it with Claude, creates Jira sub-tasks, drives each
sub-task through a GitHub PR with Claude, and rolls the parent up to
**Done** once every sub-task PR merges.

Packaged as a single Go binary (`velocity`) that listens on two
endpoints:

```
POST /webhook/jira       (assignments and status transitions)
POST /webhook/github     (pull_request merged, workflow_run failures, /velocity comments)
```

No timers, no polling loops, no manual task submission — everything
runs in response to webhook events.

For the full parent/sub-task lifecycle (states, wave math, failure,
retry, dismiss), see [**WORKFLOW.md**](./WORKFLOW.md). For contributor
rules and source-tree layout, see [**CLAUDE.md**](./CLAUDE.md).

## Contents

1. [Prerequisites](#prerequisites)
2. [Install](#install)
3. [Configure](#configure)
4. [Postgres](#postgres)
5. [Run the daemon](#run-the-daemon)
6. [Commands](#commands)
7. [Project readiness](#project-readiness)
8. [Configuration reference](#configuration-reference)
9. [Webhook configuration](#webhook-configuration)
10. [Jira custom field for repo URL](#jira-custom-field-for-repo-url)
11. [Files on disk](#files-on-disk)
12. [Dependency tracking](#dependency-tracking)
13. [Limitations](#limitations)
14. [Test](#test)
15. [Deploy](#deploy)
16. [Troubleshooting](#troubleshooting)

## Prerequisites

- macOS 12+ (arm64 or x86_64). Prebuilt release binaries ship only
  for macOS; other platforms need to build from source.
- [Go 1.24+](https://go.dev/dl/) and `make` — only required to build
  from source.
- [Docker](https://www.docker.com/) (for local Postgres via
  `compose.yml`) or any reachable Postgres 14+ instance.
- The [Claude CLI](https://claude.com/claude-code) on `PATH`, logged
  in. Velocity shells out to `claude --print` for every LLM call.
- A Jira Cloud workspace and an [Atlassian API token](https://id.atlassian.com/manage-profile/security/api-tokens)
  for the account velocity will act as.
- A GitHub personal access token with `repo` and `actions:read`
  scopes. `actions:read` lets velocity pull failing-job logs so it
  can inline the error into the iterate prompt.
- Jira `accountId` for the architect and the developer (from
  `https://<your-org>.atlassian.net/rest/api/3/myself`). The two
  may be the **same** Jira user — velocity dispatches on issue type,
  not on which ID is assigned.
- A host reachable by Jira Cloud and GitHub webhooks. Port `8000` by
  default (override in `config.yaml`). For local development, tunnel
  via `ngrok`, `cloudflared`, or `tailscale funnel`.

## Install

Both paths land the `velocity` binary in `~/.local/bin`. Make sure
that directory is on `PATH`.

### From a release (recommended)

Pick the asset for your Mac and `curl` it straight into
`~/.local/bin`:

```bash
mkdir -p ~/.local/bin

# Apple Silicon (arm64)
curl -L -o ~/.local/bin/velocity \
  https://github.com/randheer094/velocity/releases/latest/download/velocity-macos-arm64

# Intel Mac (x86_64)
curl -L -o ~/.local/bin/velocity \
  https://github.com/randheer094/velocity/releases/latest/download/velocity-macos-x86_64

chmod +x ~/.local/bin/velocity
```

### From source

```bash
git clone https://github.com/randheer094/velocity.git
cd velocity
make install        # build + move to $INSTALL_DIR (default ~/.local/bin)
```

Override the install location with `make install INSTALL_DIR=/usr/local/bin`.
Use `make build` to produce `./velocity` without installing.

## Configure

Velocity reads `~/.velocity/config.yaml` (override the data directory
with `--dir`). Copy the example and edit — it covers Jira identifiers,
per-bucket status names, LLM options per role, and server tuning. See
[Configuration reference](#configuration-reference) for the full shape.

```bash
mkdir -p ~/.velocity
cp config.example.yaml ~/.velocity/config.yaml
$EDITOR ~/.velocity/config.yaml
```

### Secrets (env vars)

Secrets are **never** in `config.yaml` — export them before
`velocity start`:

| Variable | Required | Purpose |
|---|---|---|
| `JIRA_API_TOKEN` | yes | Jira REST API auth (basic auth, paired with `jira.email`). |
| `GH_TOKEN` | yes | GitHub REST API + `git push` auth (`repo` + `actions:read` scopes). |
| `VELOCITY_DB_HOST` | yes | Postgres host (e.g. `127.0.0.1`). |
| `VELOCITY_DB_PORT` | yes | Postgres port. |
| `VELOCITY_DB_USER` | yes | Postgres user. |
| `VELOCITY_DB_PASSWORD` | yes | Postgres password. |
| `VELOCITY_DB_NAME` | yes | Postgres database name. |
| `JIRA_WEBHOOK_SECRET` | yes (prod) | Shared secret for `X-Hub-Signature`. Unset rejects every request unless `VELOCITY_INSECURE_WEBHOOKS=1`. |
| `GH_WEBHOOK_SECRET` | yes (prod) | Shared secret for `X-Hub-Signature-256`. Unset rejects every request unless `VELOCITY_INSECURE_WEBHOOKS=1`. |
| `VELOCITY_INSECURE_WEBHOOKS` | no | Set to `1` to accept unsigned webhooks (local dev with `cloudflared` / `ngrok`). Never set in production. |

Example:

```bash
export JIRA_API_TOKEN="..."
export GH_TOKEN="ghp_..."
export VELOCITY_DB_HOST="127.0.0.1"
export VELOCITY_DB_PORT="5432"
export VELOCITY_DB_USER="velocity"
export VELOCITY_DB_PASSWORD="velocity"
export VELOCITY_DB_NAME="velocity"
export JIRA_WEBHOOK_SECRET="..."
export GH_WEBHOOK_SECRET="..."
velocity start
```

## Postgres

Velocity does **not** manage its own database. Provide any Postgres
14+ instance and point velocity at it via the `VELOCITY_DB_*` env
vars above. `sslmode` is always `disable` — put a TLS-terminating
proxy in front of Postgres if you need encryption.

### Local development

The repo ships a Docker Compose file that runs Postgres 16 on port
`55432` (non-default to avoid clashes with host Postgres):

```bash
docker compose up -d postgres
# stop + remove:
docker compose down
```

Data persists in `./.pgdata/` (gitignored). Matching env vars:

```bash
export VELOCITY_DB_HOST=127.0.0.1
export VELOCITY_DB_PORT=55432
export VELOCITY_DB_USER=velocity
export VELOCITY_DB_PASSWORD=velocity
export VELOCITY_DB_NAME=velocity
```

### Schema migrations

Schema lives in `internal/db/migrations/NNNN_*.sql` and is applied
forward-only on `velocity start`. Each migration runs in its own
transaction and is recorded in `schema_migrations`. To change the
schema, add a **new** numbered file — never edit a shipped one.

## Run the daemon

```bash
velocity start          # detaches; PID at ~/.velocity/daemon.pid
velocity status         # running / stopped
velocity logs -f        # tail ~/.velocity/daemon.log
velocity stop           # SIGTERM, SIGKILL after 10 s
velocity restart        # stop + start (pick up config.yaml edits)
```

Foreground mode for debugging:

```bash
velocity start --foreground
```

All subcommands accept `--dir <path>` to target an alternate data
directory (default `~/.velocity`).

## Commands

| Command | Description |
|---|---|
| `velocity version` | Print the binary version (e.g. `velocity v0.6.0 (manifest major 0)`). |
| `velocity config` | Print the current `config.yaml` to stdout. |
| `velocity setup` | Download the velocity-resources release tarball into `~/.velocity/resources/` and pin the repo + version in `config.yaml`. Required once before `start`. |
| `velocity update-prompts [tag]` | Refresh `~/.velocity/resources/` from the configured repo. Without a tag, picks the newest release whose major matches this binary. Sends SIGHUP to a running daemon for live reload. |
| `velocity start` | Detach and run the webhook server. |
| `velocity start --foreground` | Run in the current terminal (debug). |
| `velocity stop` | SIGTERM the daemon; SIGKILL after 10 s. |
| `velocity restart` | `stop` + `start`. |
| `velocity status` | Print `running (pid N)` or `stopped`. Exit 0 if running. |
| `velocity logs` | Print `~/.velocity/daemon.log`. |
| `velocity logs -f` | Tail the log. |
| `velocity check <path>` | Report whether a project has the files velocity expects. |
| `velocity prepare <path>` | Install `CLAUDE.md` and the `prepare-for-pr` skill (Go / Android) from the local resources cache. Run `velocity setup` first to populate the cache. |
| `velocity prepare <path> --force` | Overwrite existing files when preparing. |
| `velocity --dir <path>` | Target an alternate data directory. |

## Project readiness

Before velocity opens PRs against a repo, the repo should carry a bit
of Claude-facing context. `velocity check` tells you whether it does;
`velocity prepare` installs what's missing.

### What a "ready" project has

`velocity check` verifies three things exist under **`.claude/`** at
the repo root. Nothing Claude-facing lives in the repo root itself —
it all sits under `.claude/`. The check is presence-only: contents
aren't inspected, so conventions in a freshly-prepared repo are
something the team migrates to post-onboarding.

1. **`.claude/`** — the directory that holds every Claude-facing
   file (`CLAUDE.md`, `rules/`, `skills/`).
2. **`.claude/CLAUDE.md`** — the project's high-level index for
   Claude (build / test commands, where to find rules and skills).
3. **`.claude/skills/prepare-for-pr/SKILL.md`** — the pre-PR
   checklist (format, lint, test, review the diff) Claude runs
   before opening a pull request.

### `velocity check PROJECTPATH`

Prints a per-check report and exits non-zero if anything is missing.
The detected project type is reported at the top (Go if `go.mod` is
present; Android if any of `build.gradle{,.kts}` or
`settings.gradle{,.kts}` are present). Presence-only — empty files
pass.

```
$ velocity check ./my-repo
Velocity readiness report for /abs/path/my-repo
Project type: go

  [FAIL] .claude/ directory at project root
         missing — velocity stores CLAUDE.md, skills, and rules under .claude/
  [FAIL] CLAUDE.md under .claude/ (.claude/CLAUDE.md)
         missing — create .claude/CLAUDE.md or run `velocity prepare <path>`
  [FAIL] prepare-for-pr skill installed (.claude/skills/prepare-for-pr/SKILL.md)
         missing — install with `velocity prepare <path>`

Result: NOT READY
Hint: run `velocity prepare /abs/path/my-repo` to install the missing pieces.
```

### `velocity prepare PROJECTPATH`

Detects the project type, reads the matching `<type>/` subtree from
the local resources cache at `~/.velocity/resources/`, and writes it
under `.claude/` at the project root. The cache is populated by
`velocity setup` (and refreshed by `velocity update-prompts`); if it
is missing, `prepare` exits with `resources not installed; run \`velocity setup\` first`.

The installed layout follows the resources repo: a `CLAUDE.md`
index, a set of topic files under `.claude/rules/`, and the
`prepare-for-pr` skill under `.claude/skills/`. Conventions are
what the project migrates **to** after onboarding; they're not
enforced by `velocity check`. Once a team adopts them, the
`prepare-for-pr` skill assumes the code on disk already follows
them.

`prepare` is safe to re-run: files that already exist are skipped.
Pass `--force` to overwrite them. Projects that match neither Go nor
Android are rejected — author those files by hand, or open an issue
on velocity-resources to request a new project type.

### `velocity setup` and `velocity update-prompts`

LLM prompts (arch / code / iterate) and Jira/PR failure-comment
templates live in
[velocity-resources](https://github.com/randheer094/velocity-resources)
release tarballs, not in the velocity binary. Before `velocity start`
will run, install a release with `velocity setup`:

```
$ velocity setup
Resources repo (<owner>/<repo>): randheer094/velocity-resources
Version (release tag, e.g. v0.6.0): v0.6.0
Downloading velocity-resources v0.6.0 from randheer094/velocity-resources
Installed resources at /home/you/.velocity/resources
Pinned randheer094/velocity-resources@v0.6.0 in /home/you/.velocity/config.yaml
```

`setup` downloads `velocity-resources-<tag>.tar.gz` and `SHA256SUMS`
from the release page, verifies the tarball checksum, extracts to
`~/.velocity/resources/`, and persists the repo slug + tag under
`resources:` in `config.yaml`. The major version of the tag must
match the velocity binary's major (`velocity version` prints it);
major mismatches (e.g. installing `v1.x` into a binary that supports
`v0.x`) are rejected with `major mismatch: binary expects 0, requested 1`.
The release-binary CI also gates on this — a `vX.Y.Z` release tag
that disagrees with `internal/version/VERSION` or with the
`const Major` declared in `internal/version/version.go` fails the
release build.

Cutting a release: bump `internal/version/VERSION` (and
`const Major` in `internal/version/version.go` if the major changed)
in the same commit you tag. The binary's version comes from that
file via `//go:embed`, so any build path — `make build`, plain
`go build ./cmd/velocity`, `go install`, the release CI — reports
the same value.

To upgrade or pin a different tag once the cache exists, use
`velocity update-prompts`. Without an argument it queries the
configured repo's releases API and picks the newest tag whose major
matches the binary; with an explicit tag (`velocity update-prompts v0.6.1`)
it uses that tag and rejects major mismatches the same way `setup`
does. After a successful update, if the daemon is running, the
command sends SIGHUP and the daemon swaps in the new templates with
no restart. If the daemon is not running, `update-prompts` prints
`daemon not running, restart to pick up changes` and exits 0.

The cache layout after a successful `setup` / `update-prompts`:

```
~/.velocity/
  resources/
    VERSION                    # e.g. "v0.6.0"
    SHA256SUMS                 # the verified copy
    go/.claude/...             # consumed by `velocity prepare`
    android/.claude/...
    prompts/manifest.yaml      # consumed by the daemon
    prompts/arch/plan.md
    prompts/code/run.md
    prompts/code/iterate.md
    prompts/failure/jira.md
    prompts/failure/iterate_jira.md
    prompts/failure/iterate_pr.md
```

## Configuration reference

```yaml
server:
  host: 0.0.0.0
  port: 8000
  max_concurrency: 1       # llm-queue workers (arch.Run / code.Run / code.Iterate). Default 1 = strict serial. Ops queue is always 1 worker.
  queue_size: 1024         # soft cap on pending webhook_jobs rows per queue; overflow is dropped + logged

jira:
  base_url: https://acme.atlassian.net
  email: velocity-bot@acme.com
  architect_jira_id: 712020:...
  developer_jira_id: 712020:...
  repo_url_field: customfield_10050
  task_status_map:
    new:             {default: New}
    planning:        {default: Planning}
    planning_failed: {default: Planning Failed}
    coding:          {default: Subtask In progress}
    done:            {default: Done, aliases: [Dismissed]}
  subtask_status_map:
    new:           {default: Ready for Dev}
    coding:        {default: Dev In Progress}
    coding_failed: {default: Dev Failed}
    in_review:     {default: In Review}
    done:          {default: Done, aliases: [Dismissed]}

llm:
  arch:
    provider: claude_cli
    model: claude-opus-4-6
    allowed_tools: Read Glob Grep LS
    permission_mode: bypassPermissions
    timeout_sec: 600
  code:
    provider: claude_cli
    model: claude-sonnet-4-6
    allowed_tools: Read Write Edit Glob Grep LS MultiEdit Bash
    permission_mode: bypassPermissions
    timeout_sec: 1800

resources:
  repo_slug: ""              # <owner>/<repo> of velocity-resources, written by `velocity setup`
  version: ""                # release tag currently extracted to ~/.velocity/resources, written by `velocity setup`
  fetch_timeout_sec: 30      # per-HTTP-call timeout for setup / update-prompts
```

### Status buckets

Each canonical bucket maps to one **default** Jira workflow status
(what velocity transitions *into*) plus optional case-insensitive
**aliases** that resolve back into the bucket on reads. One bucket can
absorb multiple real-world Jira statuses.

Canonical buckets:

- **Parent**: `new`, `planning`, `planning_failed`, `coding`, `done`.
- **Sub-task**: `new`, `coding`, `coding_failed`, `in_review`, `done`.

The conventional pattern adds `Dismissed` as an alias of `done`.
Cascade detection (a parent dismissal cascades to sub-tasks) keys off
the alias name; each row's `jira_status` column preserves the raw Jira
name so dismissed and merged remain distinguishable in the DB.

### Server tuning

Velocity runs **two FIFO queues** in `webhook_jobs`, separated by
the `queue` column:

- **`llm`** — carries `arch.Run`, `code.Run`, `code.Iterate` (the
  expensive Claude calls). Sized by `max_concurrency`.
- **`ops`** — carries every other kind: `arch.AdvanceWave`,
  `arch.AssignWave`, `arch.Archive`, `arch.OnDismissed`,
  `code.MarkMerged`, `code.OnDismissed`, `code.Cleanup`. Always
  one worker, so these short DB/Jira/GitHub steps never race
  each other.

Tuning notes:

- `max_concurrency = 1` (default) → strict serial LLM work. Safe
  baseline.
- `max_concurrency = N` (>1) → up to N LLM-bound jobs run in
  parallel (different keys). Raise this if your Claude / Jira /
  GitHub stacks tolerate concurrent clones + pushes.
- The ops queue worker count is hard-coded to 1.
- `queue_size` is a **soft cap** applied per queue, so a flooded
  ops backlog can't starve LLM enqueues. Enqueue checks the pending
  count for the kind's queue; if the backlog exceeds `queue_size`,
  the job is dropped and logged. Webhook senders receive `202`
  regardless.
- Each row represents **one logical step**. Handlers never inline-
  call another kind's logic — when more work is needed they
  enqueue a follow-up row and return.
- The queue is Postgres-backed (`SELECT … FOR UPDATE SKIP LOCKED`)
  and survives daemon restart — rows stuck in `running` when the
  daemon died are reset to `pending` on next start. Live view:
  `SELECT queue, status, count(*) FROM webhook_jobs GROUP BY 1, 2;`.

### LLM per-role settings

`llm.arch` and `llm.code` each configure:

- `provider` — currently only `claude_cli`.
- `model` — Claude model ID passed to `claude --model`.
- `allowed_tools` — space-separated list passed to `--allowedTools`.
- `permission_mode` — one of Claude's permission modes
  (`default`, `acceptEdits`, `bypassPermissions`).
- `timeout_sec` — hard timeout on the `claude` subprocess.

## Webhook configuration

Both endpoints support a shared-secret check. Leaving a secret
empty disables verification for that endpoint (dev only).

### Jira

Create a webhook that posts to
`http://<your-host>:<port>/webhook/jira` firing on:

- **Issue events** → *assigned*, *updated* (status transitions).

Signature: Jira Cloud signs the payload with HMAC-SHA256 and sends
the digest in `X-Hub-Signature` (format `sha256=<hex>`). Velocity
rejects mismatches with `401`.

### GitHub

Add a repository (or org) webhook posting to
`http://<your-host>:<port>/webhook/github`:

- **Content type**: `application/json`.
- **Events**:
  - *Pull requests* — `closed` with `merged=true` transitions the
    sub-task to DONE.
  - *Workflow runs* — `completed` with `conclusion=failure` and a
    non-empty `pull_requests` array triggers an iterate run on the
    PR's head branch: fresh clone, LLM rebases onto the default
    branch and resolves conflicts, fixes the failure, then the runner
    force-pushes with lease. Velocity fetches the failing-job logs
    via the Actions API, inlines the tail into the prompt, and
    derives a commit subject (`<branch>: fix CI: <first error>`).
    Runs without a PR (pushes to the default branch) are ignored.
  - *Issue comments* — a PR comment starting with `/velocity
    <instruction>` triggers the same iterate flow with the
    instruction as LLM context. Empty instructions get a usage reply;
    comments not starting with `/velocity` are ignored. The PR need
    not have been opened by velocity — any PR in a webhook-configured
    repo can be iterated.

Signature: `X-Hub-Signature-256` HMAC-SHA256. Mismatches → `401`.

## Jira custom field for repo URL

The parent ticket must carry a GitHub repository URL on a custom
field. Velocity reads this field on every webhook payload. Sub-task
payloads inherit the repo URL from the parent — nothing extra to
configure on sub-tasks.

To find the custom field ID on Atlassian Cloud, open an issue that
has the field set and visit:

```
https://<your-org>.atlassian.net/rest/api/3/issue/<KEY>?expand=names
```

The `names` map shows each `customfield_XXXXX` key alongside its
human-readable label.

## Files on disk

```
~/.velocity/
├── config.yaml
├── daemon.pid
├── daemon.log
└── workspace/<JIRA-KEY>/    per-ticket git clones (ephemeral)
```

Plan and code-task state lives in the external Postgres instance;
workspaces are removed once a ticket completes, DB rows stay as
history. Branches are named **exactly** after the sub-task Jira key
(e.g. `ENG-102`, not `feature/ENG-102`).

## Dependency tracking

Plans persist waves in order; for any sub-task you can query its
neighbouring waves:

- **predecessors** — tickets that must be DONE before this one can
  start.
- **successors** — tickets that become eligible once this one is
  DONE.

`internal/db` exposes `TaskPredecessors(ctx, jiraKey)` and
`TaskSuccessors(ctx, jiraKey)` for programmatic lookups.

## Limitations

### Prompt size

Prompts are passed to `claude --print` as the final argv, so size is
bounded by three ceilings:

- **Requirement cap (velocity-side).** The assembled
  `summary + "\n\n" + description` is capped at **150,000 characters**
  (≈ 50K tokens) before it reaches the architect LLM. Over-cap input
  is truncated with a `[…truncated to fit 250K context window]` marker
  and logged at warn. The Jira ticket is untouched — only the text
  handed to Claude is capped. Sized to leave ~200K tokens of the 250K
  window for the system prompt, tool schemas, and codebase reads.
- **OS `ARG_MAX`.** argv + env must fit under the kernel limit
  (~256 KB on macOS, ~2 MB on Linux); overflow fails with `E2BIG`.
- **Model context window.** Whatever the configured
  `llm.<role>.model` accepts; overflow surfaces as an API error.

Typical Jira tickets stay well below every limit. Unusually long
product specs should be split across parents or will have their tail
truncated at 150K characters.

## Test

```bash
make test       # unit tests; packages that need a DB skip if none configured
make vet        # go vet ./...
make test-e2e   # boots ./compose.yml postgres, runs `go test ./...`, tears down
```

`make test-e2e` wraps `scripts/test-db.sh`: starts
`docker compose up -d postgres`, waits for readiness, exports the
`VELOCITY_DB_*` set pointed at `127.0.0.1:55432`, runs the suite, and
tears the container down on exit (via `trap`, even on failure).

## Deploy

Anywhere you can run the binary and reach Jira + GitHub + Postgres
works. Two common setups:

### systemd (Linux)

`/etc/systemd/system/velocity.service`:

```ini
[Unit]
Description=velocity webhook agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=velocity
Environment=HOME=/home/velocity
EnvironmentFile=/etc/velocity/secrets.env
ExecStart=/usr/local/bin/velocity start --foreground
Restart=on-failure
RestartSec=5
ReadWritePaths=/home/velocity/.velocity

[Install]
WantedBy=multi-user.target
```

Write `/etc/velocity/secrets.env` (mode `0600`) with the env vars
from [Secrets](#secrets-env-vars), then:

```bash
sudo -u velocity cp /path/to/config.example.yaml /home/velocity/.velocity/config.yaml
sudo -u velocity $EDITOR /home/velocity/.velocity/config.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now velocity
sudo journalctl -fu velocity
```

`start --foreground` is intentional under systemd — let systemd own
the supervision, not velocity's detach path.

### launchd (macOS)

`~/Library/LaunchAgents/com.velocity.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.velocity.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/you/.local/bin/velocity</string>
    <string>start</string>
    <string>--foreground</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/Users/you/.velocity/launchd.out.log</string>
  <key>StandardErrorPath</key><string>/Users/you/.velocity/launchd.err.log</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.velocity.agent.plist
```

Secrets are read from the LaunchAgent's environment — either set
them in `EnvironmentVariables` inside the plist, or launch velocity
via a wrapper script that `export`s them first.

### Webhook reachability

Jira Cloud and GitHub must be able to reach the host. Production
deployments: front the binary with a reverse proxy (nginx, Caddy,
Traefik) and terminate TLS there. Local dev: tunnel with
`cloudflared tunnel`, `ngrok`, or `tailscale funnel`.

## Troubleshooting

| Symptom | Check |
|---|---|
| `velocity status` says stopped right after `start` | `velocity logs` — usually a config load or DB connection error. |
| `config.yaml not found` | Copy `config.example.yaml` to `~/.velocity/config.yaml` and edit. |
| DB connection fails | Verify the full `VELOCITY_DB_*` set is exported; check `docker compose ps postgres`. |
| Jira webhook returns 401 | `JIRA_WEBHOOK_SECRET` mismatch or unset. Re-export and restart; confirm the same value is set on the Jira webhook. For local dev only, `export VELOCITY_INSECURE_WEBHOOKS=1` to bypass HMAC entirely. |
| GitHub webhook returns 401 | Same as above for `GH_WEBHOOK_SECRET`. |
| Webhook returns 503 (`queue not started`) | Daemon is in startup or shutdown; retry. The same code is returned by `/healthz` if the DB pool is down — check `velocity logs` for `db ping failed`. |
| Webhook returns 413 | Payload exceeds the 5 MiB cap; check the sender (Jira ADF descriptions are usually under 1 MiB; GitHub log payloads are bounded). |
| Parent stuck in `PLANNING` | Look for `arch: stage failed` in `daemon.log`. Ticket should have been moved to `PLANNING_FAILED` with a comment. |
| Sub-task PR never opens | `code: stage failed`; usually a `git push` auth failure or a Claude timeout. Bump `llm.code.timeout_sec`. |
| Queue drops under load | `SELECT queue, status, count(*) FROM webhook_jobs GROUP BY 1, 2;` — if pending is near `server.queue_size`, raise the cap. If the LLM queue is the saturated one, also raise `server.max_concurrency`; the ops worker is always 1. |
| Schema change not picked up | Add a new `internal/db/migrations/NNNN_*.sql` and restart; migrations are forward-only. |

Full errors always land in `~/.velocity/daemon.log`. Jira comments
are secret-redacted and capped at 1000 chars — the log has the raw
output.

## Developing

```bash
make build      # → ./velocity
make install    # build + copy into ~/.local/bin
make test       # go test ./...
make test-e2e   # docker compose Postgres + full suite
make vet        # go vet ./...
make clean      # rm ./velocity
```

Module path: `github.com/randheer094/velocity`. Source tree overview
lives in [CLAUDE.md](./CLAUDE.md); contributor conventions live in
[.claude/rules/conventions.md](./.claude/rules/conventions.md).
