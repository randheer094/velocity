---
name: prepare-for-pr
description: Run before opening a PR on this Go project. Formats, vets, lints, builds, tests (unit + race), and checks coverage.
---

# Prepare for PR (Go)

Stop at the first red gate; fix, then continue. Conventions live
in [`.claude/rules/conventions.md`](../../rules/conventions.md).

1. `gofmt -w .` and `goimports -w .` — no diff after.
2. `go vet ./...`.
3. `staticcheck ./...` or `golangci-lint run` (whichever is wired).
4. `go build ./...`.
5. `go mod tidy` — no diff after.
6. `go test ./...`.
7. `go test -race ./...`.
8. `go test -cover ./...` — every package ≥ 90%.
9. DB / integration harness (`scripts/test-db.sh`, `make test-e2e`)
   if the repo has one.
10. `git diff origin/main...HEAD` — scrub debug prints, `t.Skip`,
    unjustified deps, SQL concat, new panics in library code.
11. PR: title imperative, under 70 chars. Body = what, why, how
    to verify.
