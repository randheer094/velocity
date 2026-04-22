# Project

Short description of what this Android app / library does and why
it exists.

## Build, test, run

- `./gradlew assembleDebug` — build a debug APK for every variant.
- `./gradlew test` — run unit tests across modules.
- `./gradlew connectedAndroidTest` — run instrumented / E2E tests
  on a connected device or emulator.
- `./gradlew lint` — Android lint.
- `./gradlew detekt` — Kotlin static analysis (if configured).
- `./gradlew ktlintCheck` — Kotlin style (if configured).

## Before a PR

Run the pre-PR gates documented in the project skill:
[skills/prepare-for-pr/SKILL.md](./skills/prepare-for-pr/SKILL.md).

## Conventions

Architecture (MVI + Hilt), test requirements, code style, and
module layout live in
[rules/conventions.md](./rules/conventions.md).
Read and follow them for every change.
