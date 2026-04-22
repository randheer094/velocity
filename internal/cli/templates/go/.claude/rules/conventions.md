# Conventions

Rules for this Go module. Pre-PR gates (format, vet, test, build)
live in `.claude/skills/prepare-for-pr/SKILL.md` — don't duplicate
them here.

These rules are **non-negotiable**. If a change needs to deviate
(e.g. a dependency genuinely requires a mutex pattern, or a package
can't sit under `internal/`), stop and ask the reviewer before
writing the code.

## Errors

- Library code returns `error`.
- Panic is reserved for impossible states that represent
  programmer bugs.
- Wrap with `%w` when adding context:
  `fmt.Errorf("load %s: %w", path, err)`.
- Sentinel errors are exported vars (`ErrNotFound`), checked with
  `errors.Is` / `errors.As`.
- Return the error as the last value.
- Don't log and return — the caller decides whether the error is
  worth logging.

## Concurrency

- Every blocking call accepts `context.Context` as its first
  argument and respects cancellation.
- Every goroutine has a clear owner that waits for it
  (`sync.WaitGroup`, `errgroup.Group`) or is tied to a lifecycle
  with a documented stop signal.
- Use channels + context for coordination. When a mutex is
  unavoidable, hold it for the smallest scope that's correct.
- For synchronisation, use a channel, waitgroup, or
  `context.WithTimeout`.
- `-race` must pass in CI.

## Logging / observability

- Use `log/slog` with key/value fields.
- Redact secrets at the log boundary (tokens, credentials,
  cookies).
- Errors surface with enough context to debug from the log alone:
  caller identity, inputs, wrapped cause.

## Configuration

- Secrets come from environment variables.
- Runtime config is a typed struct loaded once at startup.
- Feature flags are config.

## Dependencies

- Keep `go.mod` minimal. Every new direct dependency is justified
  in the PR body.
- Reach for the standard library first; a third-party package
  needs a clear cost argument in the PR body.
- Run `go mod tidy` before every commit. `replace` directives
  require a documented reason.

## Testing (mandatory)

- Every exported function has at least one unit test covering a
  happy path and the error path(s) a caller would hit.
- Bug fixes ship with a regression test that fails before the fix.
- Table-driven tests are the default for anything with > 1 case.
- `go test -race ./...` passes.
- External dependencies (DB, HTTP, filesystem) are exercised via a
  harness and skipped when the harness isn't available.
- Per-package statement coverage stays ≥ **90%** (a thin `main`
  shim is the only exemption).

## Security

- Validate every input at the system boundary (HTTP handler, CLI
  flag, env var, webhook payload).
- Parameterise every SQL query.
- Compare secrets with `crypto/subtle.ConstantTimeCompare`.
- HMAC / signature verification happens before any other parsing
  of untrusted input.

## Code style

- Run `gofmt` + `goimports` before every commit.
- Exported symbols have doc comments.
- Default to no inline comments; add one only when the WHY is
  non-obvious (hidden constraint, subtle invariant, bug workaround).
- Keep functions short enough that the body fits on one screen.
- Avoid package-name stutter — use names like `foo.Client`,
  `foo.New`.

## Layout

- `cmd/<binary>/` — entry points; one directory per binary. Thin
  `main` that wires flags and calls into `internal/`.
- `internal/` — packages private to this module. Most logic lives
  here.
- `pkg/` — only if other modules import it.
- `_test.go` files live next to the code they test.
