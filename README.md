# velocity

[![CI](https://img.shields.io/github/actions/workflow/status/randheer094/velocity/ci.yml?branch=main&label=CI)](https://github.com/randheer094/velocity/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/randheer094/velocity?label=release&display_name=tag)](https://github.com/randheer094/velocity/releases/latest)
[![Coverage](https://raw.githubusercontent.com/wiki/randheer094/velocity/coverage.svg)](https://raw.githack.com/wiki/randheer094/velocity/coverage.html)

Webhook-driven agent that takes a Jira parent ticket assigned to an
architect, plans it with Claude, creates Jira sub-tasks, drives each
sub-task through a GitHub PR with Claude, and rolls the parent up to
**Done** once every sub-task PR merges.

Packaged as a single Go binary (`velocity`) that listens on two
endpoints:

```
POST /webhook/jira       (assignments and status transitions)
POST /webhook/github     (pull_request merged events)
```

No timers, no polling loops, no manual task submission — everything
runs in response to webhook events.

For the full parent/sub-task lifecycle (states, wave math, failure,
retry, dismiss), see [**WORKFLOW.md**](./WORKFLOW.md). For contributor
rules and source-tree layout, see [**CLAUDE.md**](./CLAUDE.md).

## Contents

1. [Prerequisites](#prerequisites)
2. [Build](#build)
3. [Install](#install)
4. [Setup](#setup)
5. [Run the daemon](#run-the-daemon)
6. [Commands](#commands)
7. [Configuration](#configuration)
8. [Webhook configuration](#webhook-configuration)
9. [Jira custom field for repo URL](#jira-custom-field-for-repo-url)
10. [Files on disk](#files-on-disk)
11. [Dependency tracking](#dependency-tracking)
12. [Test](#test)
13. [Deploy](#deploy)
14. [Troubleshooting](#troubleshooting)

## Prerequisites

- macOS 12+ or Linux (x86_64 / arm64).
- [Go 1.24+](https://go.dev/dl/) and `make` (for building).
- The [Claude CLI](https://claude.com/claude-code) on `PATH`, logged
  in. Velocity shells out to `claude --print` for every LLM call.
- A Jira Cloud workspace and an [Atlassian API token](https://id.atlassian.com/manage-profile/security/api-tokens)
  for the account velocity will act as.
- A GitHub personal access token with `repo` scope.
- Jira `accountId` for the architect and the developer (from
  `https://<your-org>.atlassian.net/rest/api/3/myself`). The two
  may be the **same** Jira user — velocity dispatches on issue type,
  not on which ID is assigned.
- A host reachable by Jira Cloud and GitHub webhooks. Port `8000` by
  default (override in `config.json`). For local development, tunnel
  via `ngrok`, `cloudflared`, or `tailscale funnel`.

## Build

```bash
git clone https://github.com/randheer094/velocity.git
cd velocity
make build          # produces ./velocity (stripped)
```

Or straight `go build`:

```bash
go build -o velocity ./cmd/velocity
```

## Install

```bash
make install        # build + move to $INSTALL_DIR (default ~/.local/bin)
```

Override the install location:

```bash
make install INSTALL_DIR=/usr/local/bin
```

Make sure the destination is on `PATH`.

## Setup

`velocity setup` is an interactive [huh](https://github.com/charmbracelet/huh)
form. It prompts for every credential and config value and writes:

- Secrets → OS keyring under service `velocity`:
  `JIRA_API_TOKEN`, `GITHUB_TOKEN`, `JIRA_WEBHOOK_SECRET`,
  `GITHUB_WEBHOOK_SECRET`.
- Everything else → `~/.velocity/config.json`.

```bash
velocity setup          # first-time onboarding
velocity setup --edit   # re-prompt (pre-filled with existing values)
```

The form covers:

- Jira base URL (e.g. `https://acme.atlassian.net`).
- Jira user email + API token.
- Architect and developer `accountId`s.
- Jira custom field ID for the repo URL (e.g. `customfield_10050`).
- Jira + GitHub webhook shared secrets (leave blank to disable
  verification — dev only).
- GitHub personal access token.
- Per-bucket Jira status names (comma-separated list — first is the
  transition target, the rest are aliases that resolve *into* the
  bucket on reads). See [Configuration](#configuration).

## Run the daemon

```bash
velocity start          # detaches; PID at ~/.velocity/daemon.pid
velocity status         # running / stopped
velocity logs -f        # tail ~/.velocity/daemon.log
velocity stop           # SIGTERM, SIGKILL after 10 s
```

Foreground mode for debugging:

```bash
velocity start --foreground
```

Restart after hand-editing `config.json`:

```bash
velocity restart
```

All subcommands accept `--dir <path>` to target an alternate data
directory (default `~/.velocity`).

## Commands

| Command | Description |
|---|---|
| `velocity setup` | Interactive credential + config onboarding. `--edit` re-prompts. |
| `velocity start` | Detach and run the webhook server. |
| `velocity start --foreground` | Run in the current terminal (debug). |
| `velocity stop` | SIGTERM the daemon; SIGKILL after 10 s. |
| `velocity restart` | `stop` + `start`. |
| `velocity status` | Print `running (pid N)` or `stopped`. Exit 0 if running. |
| `velocity logs` | Print `~/.velocity/daemon.log`. |
| `velocity logs -f` | Tail the log. |
| `velocity --dir <path>` | Target an alternate data directory. |

## Configuration

`~/.velocity/config.json` is written by `velocity setup` and can be
hand-edited (restart with `velocity restart`). Secrets are **not**
in this file — they live in the OS keyring.

```jsonc
{
  "server": {
    "host": "0.0.0.0",
    "port": 8000,
    "max_concurrency": 1,       // workers draining the FIFO queue (default 1, strict serial)
    "queue_size": 1024          // enqueue buffer; overflow is dropped + logged
  },
  "jira": {
    "base_url": "https://acme.atlassian.net",
    "user_email": "velocity-bot@acme.com",
    "architect_account_id": "712020:...",
    "developer_account_id": "712020:...",
    "repo_url_field": "customfield_10050",
    "task_status_map": {
      "new":                 {"default": "To Do",            "aliases": ["Backlog"]},
      "planning":            {"default": "Planning"},
      "planning_failed":     {"default": "Planning Failed"},
      "subtask_in_progress": {"default": "In Progress"},
      "done":                {"default": "Done"},
      "dismissed":           {"default": "Dismissed"}
    },
    "subtask_status_map": {
      "new":         {"default": "To Do"},
      "in_progress": {"default": "In Progress", "aliases": ["Doing"]},
      "pr_open":     {"default": "In Review"},
      "code_failed": {"default": "Dev Failed"},
      "done":        {"default": "Done",        "aliases": ["Closed"]},
      "dismissed":   {"default": "Dismissed"}
    }
  },
  "llm": {
    "arch": {
      "model": "claude-opus-4-5",
      "allowed_tools": ["Read", "Grep", "Glob"],
      "permission_mode": "default",
      "timeout_sec": 600
    },
    "code": {
      "model": "claude-opus-4-5",
      "allowed_tools": ["Read", "Grep", "Glob", "Edit", "Write", "Bash"],
      "permission_mode": "acceptEdits",
      "timeout_sec": 1800
    }
  }
}
```

### Status buckets

Each canonical bucket maps to one **default** Jira workflow status
plus optional **aliases**. The default is the status velocity
transitions *into*; aliases resolve *into* the bucket on reads
(case-insensitive). One bucket can absorb multiple real-world
Jira statuses (e.g. `In Progress` and `Doing` both count as
`InProgress`).

During `velocity setup`, enter a comma-separated list per bucket —
first entry becomes the default, rest become aliases.

### Server tuning

- `max_concurrency = 1` (default) → strict serial FIFO across every
  ticket. Safe baseline.
- `max_concurrency = N` (>1) → up to N agent runs in parallel;
  dequeue order is still FIFO. Raise this if your Claude and Jira
  plans can tolerate concurrent clones + pushes.
- `queue_size` bounds the in-memory queue; overflow drops the job
  and logs an error. Webhook senders receive `202` regardless.

### LLM per-role settings

`llm.arch` and `llm.code` each configure:

- `model` — Claude model ID passed to `claude --model`.
- `allowed_tools` — list passed to `--allowedTools`.
- `permission_mode` — one of Claude's permission modes (e.g.
  `default`, `acceptEdits`, `bypassPermissions`).
- `timeout_sec` — hard timeout on the `claude` subprocess. Default
  600 s for arch, 1800 s for code.

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
- **Events**: *Pull requests* only (specifically `closed` with
  `merged=true`; velocity ignores others).

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
├── config.json
├── daemon.pid
├── daemon.log
├── data/                    embedded Postgres cluster (binaries, pgdata, runtime, cache)
└── workspace/<JIRA-KEY>/    per-ticket git clones (ephemeral)
```

All plan and code-task state lives in Postgres under `data/`. The
cluster is started and stopped by `velocity` itself — you do not
run a separate Postgres. Workspaces are removed once a ticket
completes; DB rows stay as history.

Branches are named **exactly** after the sub-task Jira key (e.g.
`ENG-102`, not `feature/ENG-102`).

### Schema changes

There is no `ALTER` / migration path. All DDL is
`CREATE TABLE IF NOT EXISTS`. To pick up new columns after an
upgrade, stop velocity and wipe `~/.velocity/data/`. Open plans
lose their DB rows; re-trigger them via Jira re-assignment.

## Dependency tracking

Every time a plan is saved, velocity derives a dependency table
from the wave structure (`plan_task_deps`). For any sub-task you
can query:

- **predecessors** — tickets that must be DONE before this one can
  start.
- **successors** — tickets that become eligible once this one is
  DONE.

The `internal/db` package exposes `TaskPredecessors(ctx, jiraKey)`
and `TaskSuccessors(ctx, jiraKey)` for programmatic lookups.

## Test

```bash
make test       # go test ./...
make vet        # go vet ./...
```

Or directly:

```bash
go test ./...
go test -race ./...
go test ./internal/webhook/...    # a single package
```

The test suite does not require Claude, Jira, or GitHub
credentials — HTTP clients are stubbed and the embedded Postgres
is spun up per-test.

## Deploy

Velocity is a single binary. Anywhere you can run the binary and
reach Jira + GitHub works. Two common setups:

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
ExecStart=/usr/local/bin/velocity start --foreground
Restart=on-failure
RestartSec=5
# velocity owns its own data dir; give it write access
ReadWritePaths=/home/velocity/.velocity

[Install]
WantedBy=multi-user.target
```

```bash
sudo -u velocity velocity setup          # one-time, interactive
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

The OS keyring is the login keychain, so the user that owns the
LaunchAgent must match the user that ran `velocity setup`.

### Webhook reachability

Jira Cloud and GitHub must be able to reach the host. Production
deployments: front the binary with a reverse proxy (nginx, Caddy,
Traefik) and terminate TLS there. Local dev: tunnel with
`cloudflared tunnel`, `ngrok`, or `tailscale funnel`.

## Troubleshooting

| Symptom | Check |
|---|---|
| `velocity status` says stopped right after `start` | `velocity logs` — usually a config load or Postgres startup error. |
| Jira webhook returns 401 | Shared secret mismatch. Re-run `velocity setup --edit` and update the Jira webhook config. |
| GitHub webhook returns 401 | Same as above for `GITHUB_WEBHOOK_SECRET`. |
| Parent stuck in `PLANNING` | Look for `arch: stage failed` in `daemon.log`. Ticket should have been moved to `PLANNING_FAILED` with a comment. |
| Sub-task PR never opens | `code: stage failed`; usually a `git push` auth failure or a Claude timeout. Bump `llm.code.timeout_sec`. |
| Queue drops under load | Raise `server.queue_size`, or `server.max_concurrency`, or both. |
| DDL changes not picked up | Wipe `~/.velocity/data/` (schema is `CREATE TABLE IF NOT EXISTS` only). |

Full errors always land in `~/.velocity/daemon.log`. Jira comments
are secret-redacted and capped at 1000 chars — the log has the raw
output.

## Developing

```bash
make build     # → ./velocity
make install   # build + copy into ~/.local/bin
make test      # go test ./...
make vet       # go vet ./...
make clean     # rm ./velocity
```

Module path: `github.com/randheer094/velocity`. Source tree overview
lives in [CLAUDE.md](./CLAUDE.md); contributor conventions live in
[.claude/rules/conventions.md](./.claude/rules/conventions.md).
