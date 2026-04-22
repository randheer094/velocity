---
name: prepare-for-pr
description: Run before opening a PR on this Android project. Uses the `android` CLI for AVDs, runs unit + connectedAndroidTest, lint, detekt/ktlint.
---

# Prepare for PR (Android)

Stop at the first red gate; fix, then continue. Conventions live
in [`.claude/rules/conventions.md`](../../rules/conventions.md).

## Boot an emulator

```bash
android sdk install platform-tools "system-images;android-34;google_apis;x86_64"
android avd create --profile pixel_6      # once
android emulator --avd <name> &
adb wait-for-device
adb shell 'while [[ -z "$(getprop sys.boot_completed)" ]]; do sleep 1; done'
```

## Gates

1. `android analyze` — module graph sane.
2. `./gradlew assembleDebug`.
3. `./gradlew test` — unit suite.
4. `./gradlew lint` — no new warnings.
5. `./gradlew detekt` / `ktlintCheck` — whichever is wired.
6. `./gradlew connectedAndroidTest` — instrumented suite.
7. `git diff origin/main...HEAD` — scrub `Log.d`/`println`,
   hardcoded strings, new permissions, new deps.
8. PR: title imperative, under 70 chars. Body = what, why, how
   to verify. Screenshots/recording for UI changes.

One-shot equivalent (with device up):

```bash
./gradlew check connectedCheck
```

See `android <cmd> --help` for CLI flags.
