---
name: prepare-for-pr
description: Run before opening a pull request on this Android project. Uses the agent-focused `android` CLI to boot devices and manage the SDK, runs the mandatory unit + E2E (connectedAndroidTest) suites, enforces MVI + Hilt conventions, and runs detekt / lint / ktlint where configured so the PR description writes itself.
---

# Prepare for PR (Android)

Run these gates in order before opening a pull request. Stop at the
first failure and fix it — do not open a PR with any step red.

**Non-negotiables for this project**

- **Architecture.** MVI (State / Intent / Effect / pure reducer) and
  Hilt for DI. See `CLAUDE.md` for the full rules.
- **Tests.** Both **unit tests** (`./gradlew test`) and **E2E /
  instrumented tests** (`./gradlew connectedAndroidTest`) are
  mandatory for every PR. Any new feature or reducer branch ships
  with a unit test; any new Composable, navigation edge, DI binding,
  or platform interaction ships with an instrumented test.

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
2. **Architecture review.** For every file this change touches or
   adds, confirm:
   - **MVI.** UI state lives on an immutable `State`. User input
     dispatches an `Intent`. One-shot side effects (nav, snackbars,
     toasts) are emitted as `Effect`s. State transitions happen in
     a pure `reduce(state, intent) -> state` — no I/O inside
     `reduce`.
   - **Hilt.** Every collaborator reaches the class via constructor
     injection (`@Inject constructor(...)`) or Hilt scope
     (`@HiltViewModel`, `@AndroidEntryPoint`). No manual `object
     ServiceLocator`, no singleton fields set from `Application`,
     no `lateinit` holders populated by hand. New bindings use
     `@Module @InstallIn(...)` with the smallest scope that works.
   If anything is off, fix before running the later gates.
3. **Build.** `./gradlew assembleDebug` must succeed for every
   module touched by this change.
4. **Unit tests (mandatory).** `./gradlew test` must exit 0. Every
   new reducer branch / use-case / mapper ships with at least one
   JVM unit test. If the change touches shared code, run the full
   suite — not just one module. Exit with a failure if the diff
   adds logic without a matching test.
5. **Android lint.** `./gradlew lint` must exit 0. Treat new
   warnings as failures unless they're explicitly baselined.
6. **Style / static analysis.** Run whichever of these the project
   configures; skip the ones that aren't wired up:
   - `./gradlew ktlintCheck`
   - `./gradlew detekt`
   - `./gradlew spotlessCheck`
7. **E2E / instrumented tests (mandatory).** Boot an AVD via the
   sequence above and run `./gradlew connectedAndroidTest`. Every
   new Composable / screen / navigation edge / DI binding ships
   with at least one instrumented test (`@HiltAndroidTest` +
   Compose UI test or Espresso). A PR that skips this gate must
   explain in the body why E2E coverage is impossible for the
   change.
8. **Diff review.** Read `git diff origin/main...HEAD`:
   - Any `Log.d`, `println`, or debug-only code left in?
   - Any hard-coded user-visible strings that should live in
     `strings.xml`?
   - Any new permission added to `AndroidManifest.xml`? Justify it.
   - Any new dependency in `build.gradle(.kts)`? Justify it.
   - Any new class that looks like a service locator or that sets
     a singleton outside Hilt? Replace it with a Hilt binding.
9. **PR draft.** Produce:
   - **Title.** Imperative mood, under 70 characters.
   - **Body.** What changed, why, and how to verify. Include
     screenshots or a recording for any user-visible UI change.
     Call out the unit + E2E tests that cover the change.

## Run-everything options

Both options run the mandatory unit **and** E2E suites. Option A is
the default; Option B is verbose for logs.

### Option A — one Gradle invocation (device/emulator up)

```bash
# Bring up an AVD via the `android` CLI (see the boot sequence above), then:
./gradlew check connectedCheck
```

`check` runs `test`, `lint`, and every configured verification task
(`detekt`, `ktlintCheck`, `spotlessCheck`, …). `connectedCheck`
runs `connectedAndroidTest` across every module with an
`androidTest/` source set. Together they cover the mandatory unit
+ E2E gates in one go.

> Do **not** ship on `./gradlew check` alone — it skips the E2E
> gate. Either run the pair above, or use Option B.

### Option B — explicit, verbose, fail-fast

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
Never drop `test` or `connectedAndroidTest` — both are mandatory.

Only open the PR once every gate above (for the option you picked)
is green.
