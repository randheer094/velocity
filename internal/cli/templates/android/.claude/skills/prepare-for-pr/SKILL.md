---
name: prepare-for-pr
description: Run before opening a pull request on this Android project. Builds, lints, tests (unit + connected via the Android CLI tools), runs detekt + Android lint where configured, and summarises the diff so the PR description writes itself.
---

# Prepare for PR (Android)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

## Android CLI tools required

The connected-test and "run all tests" options below drive the
Android CLI tools that ship with the SDK. They must be on `PATH`:

- `adb` — `$ANDROID_HOME/platform-tools/adb`.
- `emulator` — `$ANDROID_HOME/emulator/emulator`.
- `avdmanager` / `sdkmanager` — `$ANDROID_HOME/cmdline-tools/latest/bin/`.

Quick check:

```bash
command -v adb emulator avdmanager sdkmanager \
  || { echo "Missing Android CLI tools — export ANDROID_HOME and add SDK bins to PATH"; exit 1; }
adb devices
avdmanager list avd
```

Sanity sequence for connected tests from a clean shell:

```bash
# 1. Pick (or create) an AVD.
avdmanager list avd
# avdmanager create avd -n ci -k "system-images;android-34;google_apis;x86_64" -d pixel_6

# 2. Boot it headless in the background.
emulator -avd ci -no-window -no-audio -no-snapshot -no-boot-anim &

# 3. Wait until the device is ready.
adb wait-for-device
adb shell 'while [[ -z "$(getprop sys.boot_completed)" ]]; do sleep 1; done'
adb shell input keyevent 82   # unlock

# 4. Now run the connected suite.
./gradlew connectedAndroidTest
```

When finished, shut the emulator down: `adb emu kill`.

## Core gates (must all pass)

1. **Build.** `./gradlew assembleDebug` must succeed for every
   module touched by this change.
2. **Unit tests.** `./gradlew test` must exit 0. If the change
   touches shared code, run the full suite — not just one module.
3. **Android lint.** `./gradlew lint` must exit 0. Treat new
   warnings as failures unless they're explicitly baselined.
4. **Style / static analysis.** Run whichever of these the project
   configures; skip the ones that aren't wired up:
   - `./gradlew ktlintCheck`
   - `./gradlew detekt`
   - `./gradlew spotlessCheck`
5. **Instrumented tests.** If the change touches UI, navigation,
   permissions, storage, or anything device-dependent, boot an
   emulator via the sequence above and run
   `./gradlew connectedAndroidTest`.
6. **Diff review.** Read `git diff origin/main...HEAD`:
   - Any `Log.d`, `println`, or debug-only code left in?
   - Any hard-coded user-visible strings that should live in
     `strings.xml`?
   - Any new permission added to `AndroidManifest.xml`? Justify it.
   - Any new dependency in `build.gradle(.kts)`? Justify it.
7. **PR draft.** Produce:
   - **Title.** Imperative mood, under 70 characters.
   - **Body.** What changed, why, and how to verify. Include
     screenshots or a recording for any user-visible UI change.

## Run-everything options

Pick one of these when you want the whole suite in a single shot:

### Option A — every check + unit tests (no device needed)

```bash
./gradlew check
```

`check` is Gradle's aggregator: it runs `test`, `lint`, and every
verification task registered by the configured plugins (`detekt`,
`ktlintCheck`, `spotlessCheck`, …). This is the default pre-PR
sweep when the change is pure-logic.

### Option B — every check + connected tests (device/emulator up)

```bash
# Boot the emulator per the sequence above, then:
./gradlew check connectedCheck
```

`connectedCheck` runs `connectedAndroidTest` across every module
that has an `androidTest/` source set. Use this when the change
touches UI or anything that only real Android runtime can verify.

### Option C — explicit, verbose, fail-fast

Useful when you want each gate to be its own line in the log:

```bash
./gradlew assembleDebug \
  && ./gradlew test \
  && ./gradlew lint \
  && ./gradlew detekt \
  && ./gradlew ktlintCheck \
  && ./gradlew connectedAndroidTest
```

Drop `detekt` / `ktlintCheck` if the project doesn't configure them
(`./gradlew tasks --all | grep -E 'detekt|ktlintCheck'` tells you).

Only open the PR once every gate above (for the option you picked)
is green.
