# Vault Chat for Android

Native client in Kotlin and Jetpack Compose. The plan and the decisions behind
it are in [docs/android/plan.md](../docs/android/plan.md).

## Requirements

- JDK 17 or newer (the build targets Java 17 bytecode).
- Android SDK with `platforms;android-37.0`; build-tools are installed by the
  Android Gradle plugin on first build once the licences are accepted.
  Point the build at the SDK with `ANDROID_HOME` or with `sdk.dir` in
  `android/local.properties` (ignored by Git). On Windows escape the colon:
  `sdk.dir=C\:/Users/<you>/AppData/Local/Android/Sdk`.
- About 2 GB of free memory for the Gradle daemon. The daemon heap is limited
  in `gradle.properties`, and Kotlin compiles inside it.

The Gradle wrapper checks the SHA-256 of the distribution it downloads
(`distributionSha256Sum` in `gradle/wrapper/gradle-wrapper.properties`).

## Build and test

From `android/`:

```sh
./gradlew :app:testDebugUnitTest   # JVM tests: session, storage, API errors
./gradlew :app:lintRelease         # lint; warnings and errors fail the build
./gradlew :app:assembleDebug       # app/build/outputs/apk/debug/app-debug.apk
```

The debug build is installed next to a release build (`.debug` application ID
suffix). It may use plain HTTP, but only to `10.0.2.2` (the host as seen from
the emulator), `localhost` and `127.0.0.1`; release builds accept only HTTPS.

The sign-in screen offers a default server. Debug builds default to
`http://10.0.2.2:8080`, the local Compose stack. The repository carries no
deployment address: to prefill one, set it in `~/.gradle/gradle.properties`:

```properties
vaultchat.defaultServer=https://chat.example.org
```

## Release signing

`./gradlew :app:assembleRelease` without signing settings produces
`app-release-unsigned.apk`. A signed release needs a key that stays the same
for the whole life of the app: Android installs an update only if it is
signed with the same key. Losing the key means users must uninstall (and lose
local data) to get further updates.

Keep the keystore and its passwords outside the repository, with a backup
held by a second team member. Provide them through `~/.gradle/gradle.properties`
or the environment:

| Gradle property | Environment variable |
|---|---|
| `vaultchat.signing.storeFile` | `VAULTCHAT_SIGNING_STORE_FILE` |
| `vaultchat.signing.storePassword` | `VAULTCHAT_SIGNING_STORE_PASSWORD` |
| `vaultchat.signing.keyAlias` | `VAULTCHAT_SIGNING_KEY_ALIAS` |
| `vaultchat.signing.keyPassword` | `VAULTCHAT_SIGNING_KEY_PASSWORD` |

Publish the SHA-256 of the APK and of the signing certificate with each
release (`apksigner verify --print-certs app-release.apk`).

## Server requirement

The client relies on Auth accepting a repeat of the refresh token it has just
retired (PR #108, 30 s window). Against a server without it, a refresh whose
answer is lost and is repeated revokes every session of the user. See
`SessionManager` and section 5 of the plan.

## Session storage

The access token lives only in memory. The refresh token, user ID, user name
and server address are stored in `noBackupFilesDir/session.bin`, encrypted
with AES-256-GCM under a key generated in the Android Keystore
(`KeystoreSessionCipher`). Backups and device-to-device transfer exclude all
app data. A file that can never be decrypted (tampered with, or its key is
gone) is deleted with its key and the user signs in again; a passing Keystore
or disk failure keeps the file for the next start.

`KeystoreSessionCipher` itself runs only on a device; the JVM tests cover the
store with an equivalent JCE cipher. An instrumented test on an emulator is
still to be added.
