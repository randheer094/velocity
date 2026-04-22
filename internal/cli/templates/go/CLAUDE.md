# Project

Short description of what this module does and why it exists.

## Build, test, run

- `go build ./...` — compile every package.
- `go test ./...` — run the unit test suite.
- `go vet ./...` — static analysis.
- `gofmt -l .` — must be empty; run `gofmt -w .` to fix.

## Layout

- `cmd/` — entry points; each subdirectory is one binary.
- `internal/` — packages private to this module.

## Conventions

- Default to no comments. Only add one when the WHY is non-obvious.
- Don't explain WHAT — well-named identifiers do that.
- Return errors; don't panic in library code.
- Keep new features inside existing packages unless there is a clear
  reason to add a new one.
- Tests live next to the code as `*_test.go`.
