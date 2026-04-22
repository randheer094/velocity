# Conventions

Rules for this Android app / library. Pre-PR gates (build, lint,
tests) live in `.claude/skills/prepare-for-pr/SKILL.md` — don't
duplicate them here.

These rules are **non-negotiable**. If a change needs to deviate
(e.g. a third-party SDK only ships an XML view, or a feature
genuinely can't be expressed in MVI), stop and ask the reviewer
before writing the code.

## Architecture

### MVI (Model–View–Intent) — mandatory

Every screen / feature uses MVI.

- Each feature exposes a single immutable `State` (data class), a
  sealed `Intent` (user actions + lifecycle events), and a sealed
  `Effect` (one-shot side effects — navigation, snackbars, toasts).
- `ViewModel` owns a `StateFlow<State>` and a `Channel<Effect>`,
  and reduces `Intent` → `State` via a pure
  `reduce(state, intent): State`.
- I/O (network, DB, platform) lives in a use-case / processor
  layer invoked from the ViewModel.
- Views render `State` and forward user input as `Intent`.
- Every `State` field survives process death via `SavedStateHandle`.

### UI — Jetpack Compose only

All UI is Jetpack Compose.

- A single host `Activity` per app (`MainActivity`) annotated
  `@AndroidEntryPoint`.
- Navigation is Jetpack Navigation Compose.
- Composables render `State`; side effects run inside
  `LaunchedEffect` / `DisposableEffect`.
- Non-trivial Composables ship with a `@Preview`.
- Material 3 (`androidx.compose.material3`) is the design system.

### Hilt for DI — mandatory

Hilt is the DI framework.

- `Application` is `@HiltAndroidApp`. The host `Activity` is
  `@AndroidEntryPoint`. ViewModels are `@HiltViewModel`.
- Collaborators reach the class via `@Inject constructor(...)`.
- Bindings live in `@Module @InstallIn(<Component>::class)`; use
  `@Binds` for interface→impl wiring.
- Scope with intent: `@Singleton` for app-lifetime,
  `@ActivityRetainedScoped` for ViewModel-shared state,
  `@ViewModelScoped` for per-screen collaborators. Default to the
  narrowest scope that works.
- Tests use `@HiltAndroidTest` + `@UninstallModules` to swap fakes.

## Testing (mandatory)

- **Unit tests** under `src/test/`. Every reducer branch /
  use-case / mapper ships a JVM test (Turbine for `Flow`, MockK
  for collaborators).
- **Instrumented / E2E tests** under `src/androidTest/`. Every new
  Composable / screen / navigation edge / DI binding ships an
  instrumented test using `@HiltAndroidTest` + Compose UI test.
- Both suites pass before every PR.

## Code style

- Coroutines use an explicit dispatcher (`Dispatchers.IO` /
  `Default`), injected via Hilt so tests can substitute
  `StandardTestDispatcher`.
- User-visible text lives in `res/values/strings.xml`.
- Default to no comments; add one only when the WHY is non-obvious
  (hidden constraint, subtle invariant, workaround).
- Doc comments: one line where possible.

## Layout

- `app/` — main application module; wires the Hilt graph, owns
  navigation.
- `<feature>/` — feature modules; each owns its `State` / `Intent`
  / `Effect` / `ViewModel`, its Composables, and its Hilt module.
- `core/` — cross-feature code (design system, networking,
  persistence). Exposes interfaces; Hilt-bound impls live in the
  owning module.
- `build-logic/` — shared Gradle convention plugins.
