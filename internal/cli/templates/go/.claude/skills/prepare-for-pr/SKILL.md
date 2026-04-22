---
name: prepare-for-pr
description: Run before opening a pull request on this Go project. Formats, vets, tests, and summarises the diff so the PR description writes itself.
---

# Prepare for PR (Go)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

1. **Format.** `gofmt -l .` must print nothing. If it lists files,
   run `gofmt -w .` and stage the result.
2. **Vet.** `go vet ./...` must exit 0.
3. **Build.** `go build ./...` must exit 0.
4. **Test.** `go test ./...` must exit 0. If the package has a
   DB-backed harness (e.g. `scripts/test-db.sh`), run that instead.
5. **Diff review.** Read `git diff origin/main...HEAD`:
   - Any TODOs, `fmt.Println`, `log.Printf` debug lines, or
     `t.Skip` added by this change?
   - Any public API added without a doc comment?
   - Any new dependency added to `go.mod`? Justify it in the PR body.
6. **PR draft.** Produce:
   - **Title.** Imperative mood, under 70 characters.
   - **Body.** What changed, why, and how to verify. Include the
     exact commands a reviewer can run locally.

Only open the PR once every step above is green.
