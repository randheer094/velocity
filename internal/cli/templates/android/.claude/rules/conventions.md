# Conventions

Rules for this Android app / library. Pre-PR gates (build, lint,
tests) live in `.claude/skills/prepare-for-pr/SKILL.md` — don't
duplicate them here.

## Architecture

### MVI (Model–View–Intent)

- Each feature exposes a single immutable `State` (data class), a
  sealed `Intent` (user actions + lifecycle events), and a sealed
  `Effect` (one-shot side effects — navigation, snackbars, toasts).
- `ViewModel` owns a `StateFlow<State>` and a `Channel<Effect>`,
  and reduces `Intent` → `State` via a pure
  `reduce(state, intent): State`. No I/O inside `reduce`.
- I/O (network, DB, platform) lives in a use-case / processor
  layer invoked from the ViewModel, not from `reduce`.
- Views (Compose or XML) are dumb: render `State`, forward user
  input as `Intent`. No business logic in Composables, Activities,
  or Fragments.
- Every `State` field survives process death via `SavedStateHandle`
  or an equivalent persistence path.

### Hilt for DI

- Hilt is the only DI framework. No service locators, no
  hand-rolled singletons, no Koin.
- Application is `@HiltAndroidApp`. Consumers use
  `@AndroidEntryPoint` / `@HiltViewModel`. Collaborators reach the
  class via `@Inject constructor(...)`.
- Bindings live in `@Module @InstallIn(<Component>::class)`;
  prefer `@Binds` over `@Provides` for interface→impl wiring.
- Scope with intent: `@Singleton` for app-lifetime,
  `@ActivityRetainedScoped` for ViewModel-shared state,
  `@ViewModelScoped` for per-screen collaborators.
- Tests use `@HiltAndroidTest` + `@UninstallModules` to swap
  fakes — never build a parallel DI graph for tests.

## Testing (mandatory)

- **Unit tests** under `src/test/`. Every reducer branch /
  use-case / mapper ships a JVM test (Turbine for Flow, MockK or
  equivalent for collaborators).
- **Instrumented / E2E tests** under `src/androidTest/`. Every new
  Composable / screen / navigation edge / DI binding ships an
  instrumented test using `@HiltAndroidTest` + Compose UI test or
  Espresso.
- Both suites pass before every PR.

## Code style

- Prefer Jetpack Compose (or ViewBinding) over `findViewById`.
- Keep work off the main thread — coroutines with an explicit
  dispatcher (`Dispatchers.IO` / `Default`), injected via Hilt so
  tests can substitute `StandardTestDispatcher`.
- No `runBlocking` on the main thread.
- User-visible text lives in `res/values/strings.xml`. Never
  hardcode.
- Default to no comments; only add one when the WHY is non-obvious
  (hidden constraint, subtle invariant, workaround).
- Doc comments: one line where possible.

## Layout

- `app/` — main application module; wires the Hilt graph, owns
  navigation.
- `<feature>/` — feature modules; each owns its `State` / `Intent`
  / `Effect` / `ViewModel` and its Hilt module.
- `core/` or `shared/` — cross-feature code (design system,
  networking, persistence). Expose interfaces; Hilt-bind impls in
  the owning module.
- `buildSrc/` or `build-logic/` — shared Gradle convention plugins.
