---
name: prepare-for-pr
description: Run before opening a pull request on this Go project. Formats, vets, tests (including race), lints, and summarises the diff so the PR description writes itself.
---

# Prepare for PR (Go)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

**Project conventions** (errors, concurrency, logging,
dependencies, testing, security, layout) live in
[`.claude/rules/conventions.md`](../../rules/conventions.md). The
gates below assume the code you're shipping already follows them;
if it doesn't, fix it before running the gates.

## Core gates (must all pass)

1. **Format.** `gofmt -l .` must print nothing. `goimports -l .`
   too, if the project configures it. Run the `-w` variant to fix.
2. **Vet.** `go vet ./...` must exit 0.
3. **Lint.** Run `staticcheck ./...` or `golangci-lint run` if the
   project configures one of them. New warnings are failures.
4. **Build.** `go build ./...` must exit 0.
5. **Tidy.** `go mod tidy` and confirm `go.mod` / `go.sum` have no
   uncommitted changes after the run.
6. **Unit tests (mandatory).** `go test ./...` must exit 0. New
   exported functions and bug fixes ship with a test (see
   `conventions.md` §Testing).
7. **Race detector.** `go test -race ./...` must exit 0.
8. **DB / integration harness.** If the package has one
   (e.g. `scripts/test-db.sh`, `make test-e2e`), run it — the
   default `go test` often skips DB-gated tests.
9. **Coverage.** Per-package statement coverage stays ≥ 90% unless
   the package is a thin `main` shim. Run
   `go test -cover ./...` and spot-check the packages you touched.
10. **Diff review.** Read `git diff origin/main...HEAD`:
    - Any TODOs, `fmt.Println`, `log.Printf` debug lines, or
      `t.Skip` added by this change?
    - Any public API added without a doc comment?
    - Any new dependency in `go.mod`? Justify it in the PR body.
    - Any SQL built via string concat / `fmt.Sprintf`? Parameterise.
    - Any panic in library code? Return an error instead.
11. **PR draft.** Produce:
    - **Title.** Imperative mood, under 70 characters.
    - **Body.** What changed, why, and how to verify. Include the
      exact commands a reviewer can run locally.

Only open the PR once every gate above is green.
