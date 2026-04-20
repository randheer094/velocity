# Maintenance

Rules that apply to every change touching the velocity codebase.

## Docs stay in sync with code

`README.md` and `WORKFLOW.md` are part of the contract — not
optional. Any PR that changes behaviour observable in either of
them must update the doc in the same PR.

- **`README.md`** — keep the configuration reference, env-var
  table, command list, and troubleshooting matrix matched to the
  code. If a config key, env var, CLI flag, or webhook route is
  added/renamed/removed, update the README in the same change.
- **`WORKFLOW.md`** — keep the lifecycle diagrams, canonical
  bucket lists, transition tables, retry rules, and call-chain
  table matched to the code. If a status canonical, agent entry
  point, or dispatch path changes, update WORKFLOW.md in the same
  change.
- **`config.example.yaml`** — must always be a valid `config.yaml`.
  If config shape changes, this file changes with it.
- **`CLAUDE.md`** and `.claude/rules/*.md` — keep terminology
  consistent. If a canonical name is renamed (e.g. `code_failed` →
  `coding_failed`), grep the rule files and update.

When in doubt: re-read WORKFLOW.md and README.md before declaring a
task done. If either reads as stale against the diff, the task
isn't done.

## Coverage ≥ 90% per package

Every package under `internal/` must keep statement coverage at or
above **90%**. CI publishes a coverage badge; PRs that drop a
package below the threshold are rejected.

- Run `./scripts/test-db.sh -cover ./...` for the authoritative
  per-package number (the harness boots local Postgres so DB-gated
  tests actually execute).
- If a change adds a code path that's hard to cover, add a test
  for it in the same PR — don't ship the path uncovered.
- Coverage exemptions live here (none currently); adding one requires explicit justification.

`cmd/velocity` is exempt — it's a thin `main` shim with no logic
worth testing in isolation.
