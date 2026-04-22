---
name: prepare-for-pr
description: Run before opening a pull request on this Android project. Builds, lints, tests, and summarises the diff so the PR description writes itself.
---

# Prepare for PR (Android)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

1. **Build.** `./gradlew assembleDebug` must succeed for every
   module that was touched.
2. **Unit tests.** `./gradlew test` must exit 0. If the change
   touches shared code, run the full suite, not just one module.
3. **Lint.** `./gradlew lint` must exit 0. Treat new warnings as
   failures unless they're explicitly baselined.
4. **Style.** `./gradlew ktlintCheck` (or the project's configured
   formatter) must pass.
5. **Instrumented tests.** If the change touches UI, navigation, or
   anything device-dependent, run `./gradlew connectedAndroidTest`
   against an emulator.
6. **Diff review.** Read `git diff origin/main...HEAD`:
   - Any `Log.d`, `println`, or debug-only code left in?
   - Any hard-coded strings that should live in `strings.xml`?
   - Any new permission added to `AndroidManifest.xml`? Justify it.
   - Any new dependency in `build.gradle(.kts)`? Justify it.
7. **PR draft.** Produce:
   - **Title.** Imperative mood, under 70 characters.
   - **Body.** What changed, why, and how to verify. Include
     screenshots or a recording for any user-visible UI change.

Only open the PR once every step above is green.
