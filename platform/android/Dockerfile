# WeSync Android build toolchain.
#
# Pinned, OS-independent image that builds the gomobile AAR + release APK on
# Linux — identical environment locally (podman/docker) and in CI. The image
# carries ONLY the toolchain; the repo is mounted at run time (see
# docker-build.sh), so source never bakes into a layer and the image stays
# cacheable across code changes.
#
# Build:
#   podman build -f platform/android/Dockerfile -t wesync-android .
# Run (from repo root, web already built into mobile/webdist):
#   podman run --rm -v "$PWD:/src" -v "$PWD/dist/android:/out" wesync-android
#
# The committed libsyncthing.so is reused as-is (not rebuilt). web/dist must be
# built outside the container and copied into mobile/webdist beforehand.

# Fully-qualified so it resolves under podman too (no unqualified-search registries).
FROM docker.io/library/golang:1.26-bookworm

# --- Versions (keep in sync with platform/android/app/build.gradle.kts) -------
#
# UPDATING THE ANDROID TOOLCHAIN
# These versions are tightly coupled — update them together:
#
#   PLATFORM_VERSION / BUILD_TOOLS_VERSION / compileSdk / targetSdk
#     Bump annually when Google Play requires a new target API level (usually
#     ~1 year after a new Android release). Update here AND in app/build.gradle.kts.
#     Google publishes the requirement at: https://developer.android.com/google/play/requirements/target-sdk
#
#   NDK_VERSION + GOMOBILE_VERSION
#     gomobile is pinned to a specific NDK — never bump one without the other.
#     Find a tested pair in the golang.org/x/mobile release notes or CHANGELOG.
#     After bumping, rebuild libsyncthing.so and commit it.
#
#   CMDLINE_TOOLS_VERSION
#     Safe to bump independently; just a download ID for the Android SDK CLI tools.
#     Find the latest at: https://developer.android.com/studio#command-line-tools-only
#
ENV ANDROID_SDK_ROOT=/opt/android-sdk \
    ANDROID_HOME=/opt/android-sdk \
    NDK_VERSION=28.0.13004108 \
    BUILD_TOOLS_VERSION=34.0.0 \
    PLATFORM_VERSION=android-34 \
    CMDLINE_TOOLS_VERSION=14742923 \
    # Same pseudo-version that go.mod pins for golang.org/x/mobile.
    GOMOBILE_VERSION=v0.0.0-20260602190626-68735029466e \
    DEBIAN_FRONTEND=noninteractive

# --- JDK 17 + tooling ---------------------------------------------------------
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        openjdk-17-jdk-headless unzip git ca-certificates && \
    rm -rf /var/lib/apt/lists/*
ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

# --- Android SDK command-line tools ------------------------------------------
RUN mkdir -p "${ANDROID_SDK_ROOT}/cmdline-tools" && \
    curl -fsSL -o /tmp/cmdline-tools.zip \
        "https://dl.google.com/android/repository/commandlinetools-linux-${CMDLINE_TOOLS_VERSION}_latest.zip" && \
    unzip -q /tmp/cmdline-tools.zip -d "${ANDROID_SDK_ROOT}/cmdline-tools" && \
    mv "${ANDROID_SDK_ROOT}/cmdline-tools/cmdline-tools" "${ANDROID_SDK_ROOT}/cmdline-tools/latest" && \
    rm /tmp/cmdline-tools.zip
ENV PATH="${ANDROID_SDK_ROOT}/cmdline-tools/latest/bin:${ANDROID_SDK_ROOT}/platform-tools:${PATH}"

# --- SDK packages: platform, build-tools, NDK --------------------------------
RUN yes | sdkmanager --licenses >/dev/null && \
    sdkmanager --install \
        "platform-tools" \
        "platforms;${PLATFORM_VERSION}" \
        "build-tools;${BUILD_TOOLS_VERSION}" \
        "ndk;${NDK_VERSION}" >/dev/null
ENV ANDROID_NDK_HOME="${ANDROID_SDK_ROOT}/ndk/${NDK_VERSION}"

# --- gomobile / gobind (pinned to the version go.mod requires) ---------------
RUN go install "golang.org/x/mobile/cmd/gomobile@${GOMOBILE_VERSION}" && \
    go install "golang.org/x/mobile/cmd/gobind@${GOMOBILE_VERSION}"
# golang image already puts GOPATH/bin (/go/bin) on PATH.

# UTF-8 locale so javac (invoked by gomobile bind) reads the generated Java —
# which carries non-ASCII chars like em-dash from Go doc comments — as UTF-8.
# Without this the container's POSIX C locale defaults javac to US-ASCII and the
# bind fails with "unmappable character" errors. Placed after the heavy layers
# to keep the NDK/gomobile cache intact.
ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

WORKDIR /src
COPY platform/android/docker-build.sh /usr/local/bin/docker-build.sh
# Strip any CR (the repo may be checked out with CRLF on Windows) and make exec.
RUN sed -i 's/\r$//' /usr/local/bin/docker-build.sh && \
    chmod +x /usr/local/bin/docker-build.sh

ENTRYPOINT ["/usr/local/bin/docker-build.sh"]
