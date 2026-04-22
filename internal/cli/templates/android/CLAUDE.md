# Project

Short description of what this Android app / library does and why it
exists.

## Build, test, run

- `./gradlew assembleDebug` — build a debug APK for every variant.
- `./gradlew test` — run unit tests across modules.
- `./gradlew connectedAndroidTest` — run instrumented tests on a
  connected device or emulator.
- `./gradlew lint` — Android lint.
- `./gradlew ktlintCheck` — Kotlin style (if configured).

## Layout

- `app/` — main application module.
- `<feature>/` — feature or library modules; each owns its own
  `build.gradle(.kts)`.
- `buildSrc/` or `build-logic/` — shared Gradle convention plugins
  (if present).

## Conventions

- Prefer Jetpack Compose or ViewBinding over `findViewById`.
- Keep work off the main thread; use coroutines with an explicit
  dispatcher (`Dispatchers.IO` / `Default`).
- No `runBlocking` on the main thread.
- Resources (strings, dimens, colors) go under `res/values/`; never
  hard-code user-visible text in code.
- Default to no comments. Only add one when the WHY is non-obvious.
- Tests live under `src/test/` (unit) and `src/androidTest/`
  (instrumented).
