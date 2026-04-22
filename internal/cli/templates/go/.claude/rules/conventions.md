# Conventions

Rules for this Go module. Pre-PR gates (format, vet, test, build)
live in `.claude/skills/prepare-for-pr/SKILL.md` — don't duplicate
them here.

## Errors

- Library code returns `error`. Panic is reserved for impossible
  states that represent programmer bugs, never for expected
  failures.
- Wrap with `%w` when adding context:
  `fmt.Errorf("load %s: %w", path, err)`.
- Sentinel errors are exported vars (`ErrNotFound`), checked with
  `errors.Is` / `errors.As`. Never string-match on error messages.
- Don't log and return — the caller decides whether the error is
  worth logging.
- Return the error as the last value. Boolean "did this succeed"
  return values are a smell.

## Concurrency

- Every blocking call accepts `context.Context` as its first
  argument and respects cancellation.
- Every goroutine has a clear owner that waits for it
  (`sync.WaitGroup`, `errgroup.Group`) or is tied to a lifecycle
  with a documented stop signal. No fire-and-forget goroutines.
- Prefer channels + context over manual mutexes. When a mutex is
  unavoidable, hold it for the smallest scope that's correct.
- Never use `time.Sleep` for synchronisation — use a channel,
  waitgroup, or `context.WithTimeout`.
- `-race` must pass in CI.

## Logging / observability

- Structured logging only (`log/slog`). Fields are key/value
  pairs; never interpolate into the message.
- Redact secrets at the log boundary (tokens, credentials, cookies).
- Errors surface with enough context to debug from the log alone:
  caller identity, inputs, wrapped cause.

## Configuration

- Secrets come from environment variables, never from disk files
  checked into the repo.
- Runtime config is a typed struct loaded once at startup. No
  mid-flight reloads unless explicitly designed for it.
- Feature flags are config — not runtime state mutated at random.

## Dependencies

- Minimal `go.mod`. Every new direct dependency is justified in
  the PR body.
- Prefer the standard library. Reach for a third-party package
  only when its cost is clearly lower than reimplementing.
- `go mod tidy` before every commit. `replace` directives stay
  out of the committed `go.mod` unless the reason is documented.

## Testing (mandatory)

- Every exported function has at least one unit test covering a
  happy path and the error path(s) a caller would hit.
- Bug fixes ship with a regression test that fails before the fix.
- Table-driven tests are the default for anything with > 1 case.
- `go test -race ./...` passes.
- External dependencies (DB, HTTP, filesystem) are exercised via a
  harness and skipped when the harness isn't available. Don't mock
  at the stdlib layer.
- Keep per-package statement coverage at or above **90%** unless
  the package is a thin `main` shim.

## Security

- Validate every input at the system boundary (HTTP handler, CLI
  flag, env var, webhook payload). Trust internal callers.
- Parameterise every SQL query; never build SQL by string
  concatenation or `fmt.Sprintf`.
- Compare secrets with `crypto/subtle.ConstantTimeCompare` to
  avoid timing attacks.
- HMAC / signature verification happens before any other parsing
  of untrusted input.

## Code style

- `gofmt` + `goimports`. No hand-formatting.
- Exported symbols have doc comments. Unexported ones usually
  don't.
- Default to no inline comments; only add one when the WHY is
  non-obvious (hidden constraint, subtle invariant, bug workaround).
- Keep functions short. If the whole body doesn't fit on one
  screen, split it.
- Don't stutter: `foo.Foo` is a smell. `foo.New`, `foo.Client` are
  fine.

## Layout

- `cmd/<binary>/` — entry points; one directory per binary. Thin
  `main` that wires flags and calls into `internal/`.
- `internal/` — packages private to this module. Most logic lives
  here.
- `pkg/` — only if other modules import it. Most modules don't
  need one.
- `_test.go` files live next to the code they test.
