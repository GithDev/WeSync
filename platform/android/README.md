# WeSync Android

The Go backend cross-compiles for android/arm64 and is bundled as an AAR; a
single Activity hosts a WebView pointed at the in-process Go HTTP server.
Syncthing runs on-device (bundled as `app/src/main/jniLibs/arm64-v8a/libsyncthing.so`,
launched by the Go backend), with a foreground service (`WeSyncService.kt`), a power
gate (`PowerController.kt`/`PowerLogic.kt`), boot/trigger receivers, and the runtime
permission flow.

## One-command build (recommended)

From the repo root (Docker/podman via the pinned toolchain image — see the Makefile):

```sh
make web        # build the React frontend once (the APK embeds it)
make android    # refresh webdist + regen aar + assembleDebug
```
Output: `dist\android\app-debug.apk`.

## Build the AAR (manual)

The AAR is **gitignored** (`platform/android/app/libs/*.aar`) — it's a ~12 MB compiled
snapshot of `./mobile` + the Go packages it imports, so it must be **regenerated
after any Go change** (rebuild <10s) or those changes never reach the APK:

```powershell
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android\Sdk"
$env:ANDROID_NDK_HOME = "$env:ANDROID_HOME\ndk\28.0.13004108"
$env:PATH = "$env:USERPROFILE\go\bin;$env:PATH"
Set-Location C:\code\st
# The embedded UI is copied from web/dist into mobile/webdist before bind
# (see mobile/static.go) — build the web first, or use `make android` which does it.
gomobile bind -target=android/arm64 -androidapi=21 -o platform\android\app\libs\wesync.aar ./mobile
```

## Build the APK (Android Studio)

1. Open Android Studio → **Open** → select `c:\code\st\platform\android`
2. Wait for Gradle sync (Studio downloads its own Gradle on first run)
3. Plug phone in with USB debugging enabled
4. Click the green **Run** button (▶) — Studio installs and launches the APK

To produce a standalone APK file for side-loading instead:
- **Build → Build Bundle(s) / APK(s) → Build APK(s)**
- The APK lands at `app\build\outputs\apk\debug\app-debug.apk`
- Copy to phone via USB, ADB (`adb install app-debug.apk`), or any file
  transfer

## Build the APK (CLI)

Once Studio has set up the Gradle wrapper (first sync), you can build
from the terminal:

```powershell
cd c:\code\st\platform\android
.\gradlew assembleDebug
```

APK at: `app\build\outputs\apk\debug\app-debug.apk`
