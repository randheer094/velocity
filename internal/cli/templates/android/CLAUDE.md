# Project

Short description of what this Android app / library does and why it
exists.

## Build, test, run

- `./gradlew assembleDebug` — build a debug APK for every variant.
- `./gradlew test` — run unit tests across modules. **Mandatory**
  before every PR.
- `./gradlew connectedAndroidTest` — run instrumented / end-to-end
  tests on a connected device or emulator. **Mandatory** when the
  change touches UI, navigation, DI wiring, persistence, or anything
  else the JVM unit suite can't exercise.
- `./gradlew lint` — Android lint.
- `./gradlew detekt` — Kotlin static analysis (if configured).
- `./gradlew ktlintCheck` — Kotlin style (if configured).

## Architecture

This project is built around two non-negotiable pillars:

### MVI (Model–View–Intent)

- Each screen / feature exposes a single `State` (immutable data
  class), a sealed `Intent` (user actions and lifecycle events), and
  a sealed `Effect` (one-shot side effects like navigation or
  snackbars).
- A `ViewModel` owns a `StateFlow<State>` and a `Channel<Effect>`.
  It reduces `Intent`s into new `State`s via a pure `reduce(state,
  intent) -> state` function; side effects (network, DB, platform)
  live in a dedicated `Reducer` / `Processor` or use-case layer.
- Views (Compose or XML) are dumb: they render `State` and forward
  user input as `Intent`s. No business logic in Composables,
  Activities, or Fragments.
- State is serialisable — every new field must be supported by
  `SavedStateHandle` or an equivalent persistence path.

### Hilt for DI

- Hilt is the only dependency-injection framework. No manual service
  locators, no Dagger-2 modules outside Hilt's graph, no Koin.
- Application entry point is annotated `@HiltAndroidApp`.
  Activities/Fragments/ViewModels use `@AndroidEntryPoint` /
  `@HiltViewModel`.
- Provide bindings via `@Module @InstallIn(<Component>::class)`.
  Prefer `@Binds` over `@Provides` for interface-to-impl wiring.
- Scope with intent: `@Singleton` for app-lifetime graphs,
  `@ActivityRetainedScoped` for ViewModel-shared state,
  `@ViewModelScoped` for per-screen collaborators.
- Tests use `@HiltAndroidTest` + `@UninstallModules` to swap real
  bindings for fakes. Never build a parallel DI graph for tests.

## Layout

- `app/` — main application module (wires the Hilt graph, owns
  navigation).
- `<feature>/` — feature modules. Each feature ships its own
  `State` / `Intent` / `Effect` / `ViewModel` and its own Hilt
  module.
- `core/` or `shared/` — cross-feature code (design system,
  networking, persistence). Expose interfaces; Hilt-bind the impls
  in the owning module.
- `buildSrc/` or `build-logic/` — shared Gradle convention plugins
  (if present).

## Conventions

- Prefer Jetpack Compose or ViewBinding over `findViewById`.
- Keep work off the main thread; use coroutines with an explicit
  dispatcher (`Dispatchers.IO` / `Default`). Inject the dispatcher
  via Hilt so tests can substitute `StandardTestDispatcher`.
- No `runBlocking` on the main thread.
- Resources (strings, dimens, colors) go under `res/values/`; never
  hard-code user-visible text in code.
- Default to no comments. Only add one when the WHY is non-obvious.
- Tests live under `src/test/` (unit — pure JVM, Turbine + MockK or
  equivalent) and `src/androidTest/` (instrumented / E2E — Hilt
  test graph, Espresso or Compose UI test). Every new feature ships
  both.
