#!/usr/bin/env bash
# In-container build: gomobile AAR -> gradle APK. Runs with the repo mounted at
# /src (WORKDIR). Usage:
#   docker-build.sh [gradle-task]      # default: assembleRelease
#
# Web is expected to be prebuilt into mobile/webdist (mobile/static.go embeds
# it). We do NOT run npm here — build web/dist outside and copy it in first.
set -euo pipefail

GRADLE_TASK="${1:-assembleRelease}"

cd /src

# Mark /src safe so git commands (e.g. in build.gradle.kts) work when the repo
# is owned by a different UID than the container user (mounted Windows tree).
git config --global --add safe.directory /src 2>/dev/null || true

# --- preconditions -----------------------------------------------------------
if [ ! -f mobile/webdist/index.html ]; then
    echo "ERROR: mobile/webdist/index.html missing." >&2
    echo "Build the web UI first (npm run build in web/) and copy web/dist -> mobile/webdist." >&2
    exit 1
fi
if [ ! -f platform/android/app/src/main/jniLibs/arm64-v8a/libsyncthing.so ]; then
    echo "ERROR: libsyncthing.so missing from jniLibs." >&2
    exit 1
fi

# --- 1. gomobile bind: ./mobile -> AAR ---------------------------------------
echo "[docker-build] gomobile bind -> wesync.aar"
mkdir -p platform/android/app/libs
gomobile bind -target=android/arm64 -androidapi=21 \
    -o platform/android/app/libs/wesync.aar ./mobile

# --- 2. gradle: build the APK ------------------------------------------------
# Invoke the wrapper JAR directly instead of ./gradlew. The shell script breaks
# when the repo is checked out with CRLF line endings (Windows working tree
# mounted into the container) — the loader can't find "/usr/bin/env sh\r". Going
# straight to GradleWrapperMain sidesteps the shebang entirely and behaves
# identically on a Linux CI checkout.
echo "[docker-build] gradle ${GRADLE_TASK} (via wrapper jar)"
cd platform/android
# Keep Gradle's lock/cache files off the mounted source tree. When the repo is a
# Windows working tree mounted over 9p (local podman/WSL), Gradle's file-locking
# (RandomAccessFile + FileLock on <project>/.gradle) fails with EIO because 9p
# doesn't support those semantics. Redirecting the project cache to the
# container's native filesystem avoids it. Harmless on a native CI checkout.
# Overridable so CI can point at a cached location.
GRADLE_PROJECT_CACHE_DIR="${GRADLE_PROJECT_CACHE_DIR:-/tmp/gradle-project-cache}"
# `clean` first so a fresh build never mixes with stale app/build intermediates
# left in the mounted tree by a previous (e.g. Windows) build — that mismatch
# breaks resource linking when the project cache is relocated.
java -Dorg.gradle.appname=gradlew \
    -classpath gradle/wrapper/gradle-wrapper.jar \
    org.gradle.wrapper.GradleWrapperMain --no-daemon \
    --project-cache-dir "${GRADLE_PROJECT_CACHE_DIR}" \
    clean "${GRADLE_TASK}"

# --- 3. surface the APK ------------------------------------------------------
# assembleDebug -> apk/debug, assembleRelease -> apk/release
case "${GRADLE_TASK}" in
    *Release*) APK_DIR="app/build/outputs/apk/release" ;;
    *)         APK_DIR="app/build/outputs/apk/debug" ;;
esac

shopt -s nullglob
apks=("${APK_DIR}"/*.apk)
if [ ${#apks[@]} -eq 0 ]; then
    echo "ERROR: no APK produced in ${APK_DIR}" >&2
    exit 1
fi

echo "[docker-build] APK(s) built:"
for apk in "${apks[@]}"; do
    echo "  ${apk}  ($(du -h "${apk}" | cut -f1))"
done

# Copy to /out if mounted (CI / convenience), otherwise leave in build tree.
if [ -d /out ]; then
    cp "${apks[@]}" /out/
    echo "[docker-build] copied to /out/"
fi
