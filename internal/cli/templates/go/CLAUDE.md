# Project

Short description of what this Go module does and why it exists.

## Build, test, run

- `go build ./...` — compile every package.
- `go test ./...` — run the unit test suite.
- `go test -race ./...` — race detector; must pass in CI.
- `go vet ./...` — static analysis.
- `gofmt -l .` — must print nothing; run `gofmt -w .` to fix.

## Before a PR

Run the pre-PR gates documented in the project skill:
[.claude/skills/prepare-for-pr/SKILL.md](./.claude/skills/prepare-for-pr/SKILL.md).

## Conventions

Error handling, concurrency rules, logging, configuration,
dependencies, test requirements, security, code style, and module
layout live in
[.claude/rules/conventions.md](./.claude/rules/conventions.md).
Read and follow them for every change.
