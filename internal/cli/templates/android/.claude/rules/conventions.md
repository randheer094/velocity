# Conventions

Rules for this Android app / library. Pre-PR gates (build, lint,
tests) live in `.claude/skills/prepare-for-pr/SKILL.md` — don't
duplicate them here.

These are the top-level rules for the project. Follow them.

## Architecture

### MVI (Model–View–Intent)

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
- `SavedStateHandle` carries only the fields needed to reload the
  screen after process death (e.g. the `id` on a details page);
  derived or fetched data is re-loaded on restart.

### UI: Jetpack Compose

All UI is Jetpack Compose.

- A single host `Activity` per app (`MainActivity`) annotated
  `@AndroidEntryPoint`.
- Navigation uses Navigation 3 (`androidx.navigation3`).
- Composables render `State`; side effects run inside
  `LaunchedEffect` / `DisposableEffect`.
- Non-trivial Composables ship with a `@Preview`.
- Material 3 (`androidx.compose.material3`) is the design system.

### DI: Hilt

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

## Testing

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

## Build

- Gradle wrapper is checked in; every invocation goes through
  `./gradlew`.
- Build scripts are Kotlin DSL (`build.gradle.kts`).
- Every dependency and version lives in
  `gradle/libs.versions.toml`; module scripts reference catalog
  aliases.
- Android / Kotlin / Compose configuration lives in convention
  plugins under `build-logic/`. Feature modules apply plugins,
  not raw config blocks.
- JVM toolchain is pinned in the convention plugin
  (`kotlin { jvmToolchain(17) }`).
- Kotlin compilation runs with `allWarningsAsErrors = true`.
- Android lint runs with `abortOnError = true`.
- Dependency versions in the catalog are exact (no `+`, no
  `latest.release`).
- `release` builds enable R8 and resource shrinking.
- Signing config reads keystore path and passwords from env vars
  injected by CI; keystores aren't committed.

## Layout

- `app/` — main application module; wires the Hilt graph, owns
  navigation.
- `<feature>/` — feature modules; each owns its `State` / `Intent`
  / `Effect` / `ViewModel`, its Composables, and its Hilt module.
- `core/` — cross-feature code (design system, networking,
  persistence). Exposes interfaces; Hilt-bound impls live in the
  owning module.
- `build-logic/` — shared Gradle convention plugins.
