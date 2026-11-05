# Android client: releases

Stage 8 of the [plan](plan.md). The app is distributed only as a signed APK
in GitHub Releases.

## Updates

Once a day, when the room list opens, the app reads the repository's
releases from the GitHub API and offers the newest release tagged
`android-v<version>` that is newer than the installed `versionName`
(drafts and prereleases are skipped). It shows a banner whose button opens
the release page in the browser; the user downloads and installs the APK
there. The app never downloads or installs anything by itself, and opens
only the page `https://github.com/AlexandrKhromov2005/vault-chat/releases/tag/<tag>`
of the release, spelled exactly so. Any answer from GitHub waits for the next
day; a cancelled check or no connection tries again the next time the room list
opens. Only a list of releases changes the offer, so an error, an answer that is
not the list or one over the 1 MiB limit keeps an offer already found. A check
time ahead of the clock does not hold back the next check.

Android installs an update only if it is signed with the same key, so the
key must never change (see below).

## Making a release

1. **Key, once.** Create the signing key outside the repository and keep a
   backup of it and its passwords somewhere safe; a lost key means users
   must uninstall the app (and lose its local data) to get a new one:

   ```sh
   keytool -genkeypair -v -keystore vaultchat-release.jks -alias vaultchat \
     -keyalg RSA -keysize 4096 -validity 10000
   ```

   Then set `vaultchat.signing.*` in `~/.gradle/gradle.properties` as in
   [android/README.md](../../android/README.md), and `vaultchat.defaultServer`
   and `vaultchat.firebase.*` if wanted.
2. **Version.** Raise `versionCode` by one and set `versionName` in
   `android/app/build.gradle.kts`; commit.
3. **Build and check.**

   ```sh
   cd android
   ./gradlew :app:testDebugUnitTest :app:lintRelease :app:assembleRelease
   apksigner verify --print-certs app/build/outputs/apk/release/app-release.apk
   sha256sum app/build/outputs/apk/release/app-release.apk
   ```

4. **Publish.** Check that the tag says what the APK says, tag the commit and
   attach the APK. The app offers a release by its tag, so a tag that differs
   from `versionName` offers the wrong version, or the installed one forever.
   Put both SHA-256 values (APK and signing certificate) in the notes.
   `--verify-tag` refuses to go on without the pushed tag, instead of creating
   one from the default branch. The chain stops at the first step that fails:

   ```sh
   tag=android-v0.1.0
   apk=app/build/outputs/apk/release/app-release.apk
   version=$(sed -n 's/.*versionName = "\(.*\)"/\1/p' app/build.gradle.kts)
   test "$tag" = "android-v$version" \
     && aapt2 dump badging "$apk" | grep -q "versionName='$version'" \
     && git tag "$tag" && git push origin "$tag" \
     && gh release create "$tag" "$apk" --verify-tag \
       --title "Vault Chat for Android $version" --notes "APK SHA-256: … Certificate SHA-256: …"
   ```

## Not done yet

- Baseline Profiles and checks on real devices and on the API 26 emulator:
  no device or emulator was available while building the app. The release
  build with R8 was built, not run.
- The first release itself, which needs the signing key.
