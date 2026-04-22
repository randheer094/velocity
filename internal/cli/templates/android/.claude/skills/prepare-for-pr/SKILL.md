---
name: prepare-for-pr
description: Run before opening a pull request on this Android project. Uses the agent-focused `android` CLI to boot devices and manage the SDK, runs unit + connected tests, and runs detekt / lint / ktlint where configured so the PR description writes itself.
---

# Prepare for PR (Android)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

## Android CLI (agent-focused wrapper)

This skill drives the **`android`** CLI — the agent-first wrapper
documented at <https://developer.android.com/tools/agents/android-cli>.
It's the preferred entry point for AVD and SDK management in agent
workflows; it replaces most direct calls to `avdmanager`,
`sdkmanager`, and `emulator`. `adb` is still used for device-side
interaction (`wait-for-device`, logcat, shell).

### Install / verify

```bash
# macOS arm64
curl -fsSL https://dl.google.com/android/cli/latest/darwin_arm64/install.sh | bash
# (see the docs page for other platforms)

android --version
android info                       # prints the resolved ANDROID_HOME
command -v adb || { echo "adb not on PATH — run `android sdk install platform-tools`"; exit 1; }
```

### Subcommands this skill uses

- `android analyze` — emits project metadata (modules, build targets,
  output artifact paths) as JSON. Run once early to confirm the agent
  sees the same module graph Gradle does.
- `android sdk install <pkg>` — install missing SDK packages
  (`platform-tools`, `system-images;android-34;google_apis;x86_64`,
  …).
- `android avd list` / `android avd create` — enumerate or create an
  AVD for instrumented tests.
- `android emulator` — launch the AVD (use its `--help` for the
  headless / no-audio flags your CI needs).
- `android docs search <query>` / `android docs fetch <kb://…>` —
  pull authoritative Android docs into the agent context when the
  change needs platform-API guidance.
- `android skills add` / `android skills init` — install or refresh
  Android skills into your agent's skills directory.

Always prefer `android <cmd> --help` for the exact flags your
installed version supports — the CLI is young (v0.7, April 2026) and
flags can move.

### Boot an AVD for connected tests

```bash
# 1. Make sure platform-tools + a system image are present.
android sdk install platform-tools
android sdk install "system-images;android-34;google_apis;x86_64"

# 2. Create (or reuse) an AVD.
android avd list
android avd create --profile pixel_6      # skip if one already exists

# 3. Boot it headless.
android emulator --avd <name> &           # see `android emulator --help`

# 4. Wait until the device is fully booted before kicking off tests.
adb wait-for-device
adb shell 'while [[ -z "$(getprop sys.boot_completed)" ]]; do sleep 1; done'
adb shell input keyevent 82               # unlock

# 5. Run the connected suite.
./gradlew connectedAndroidTest

# 6. Tear down.
adb emu kill
```

## Core gates (must all pass)

1. **Analyze.** `android analyze` — confirms the agent's view of the
   module graph matches Gradle. Fix any mismatch before continuing.
2. **Build.** `./gradlew assembleDebug` must succeed for every
   module touched by this change.
3. **Unit tests.** `./gradlew test` must exit 0. If the change
   touches shared code, run the full suite — not just one module.
4. **Android lint.** `./gradlew lint` must exit 0. Treat new
   warnings as failures unless they're explicitly baselined.
5. **Style / static analysis.** Run whichever of these the project
   configures; skip the ones that aren't wired up:
   - `./gradlew ktlintCheck`
   - `./gradlew detekt`
   - `./gradlew spotlessCheck`
6. **Instrumented tests.** If the change touches UI, navigation,
   permissions, storage, or anything device-dependent, boot an AVD
   via the sequence above and run `./gradlew connectedAndroidTest`.
7. **Diff review.** Read `git diff origin/main...HEAD`:
   - Any `Log.d`, `println`, or debug-only code left in?
   - Any hard-coded user-visible strings that should live in
     `strings.xml`?
   - Any new permission added to `AndroidManifest.xml`? Justify it.
   - Any new dependency in `build.gradle(.kts)`? Justify it.
8. **PR draft.** Produce:
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
`ktlintCheck`, `spotlessCheck`, …). Default pre-PR sweep when the
change is pure-logic.

### Option B — every check + connected tests (device/emulator up)

```bash
# Bring up an AVD via the `android` CLI (see the boot sequence above), then:
./gradlew check connectedCheck
```

`connectedCheck` runs `connectedAndroidTest` across every module
with an `androidTest/` source set. Use this when the change touches
UI or anything that only a real Android runtime can verify.

### Option C — explicit, verbose, fail-fast

Useful when you want each gate to be its own line in the log:

```bash
android analyze \
  && ./gradlew assembleDebug \
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
