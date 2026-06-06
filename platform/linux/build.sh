#!/usr/bin/env bash
# In-container Linux build. Runs with the repo mounted at /src (WORKDIR) and the
# output dir mounted at /out. Mirrors platform/android/docker-build.sh's contract.
#
# Usage:  build.sh [target]
#   service   wesync          — backend service binary (pure Go, CGO_ENABLED=0)
#   gui       wesync-app      — Wails desktop GUI (CGO + GTK3/WebKit2)
#   windows   installer       — wesync.exe + wesync-app.exe + syncthing.exe + NSIS setup
#   all       service + gui + bundled syncthing   (default)
#
# Web is expected to be prebuilt into web/dist (root static.go embeds it). We do
# NOT run the frontend build here — do `make web` first (or `make all`).
set -euo pipefail

TARGET="${1:-all}"
SYNCTHING_VERSION="${SYNCTHING_VERSION:-v2.1.1}"
SYNCTHING_ARCH="${SYNCTHING_ARCH:-amd64}"

cd /src
mkdir -p /out

# Build stamp injected into wesync via -ldflags (-X wesync/internal/api.BuildTime),
# surfaced by GET /api/status so every device shows exactly which build it runs.
git config --global --add safe.directory /src 2>/dev/null || true
GIT_SHA="$(git -C /src rev-parse --short HEAD 2>/dev/null || echo nogit)"
if [ -n "$(git -C /src status --porcelain 2>/dev/null)" ]; then GIT_SHA="${GIT_SHA}-dirty"; fi
BUILD_STAMP="$(date -u +%Y%m%d-%H%M%S)-${GIT_SHA}"
LD_COMMON="-s -w -X wesync/internal/api.BuildTime=${BUILD_STAMP}"
echo "[build] stamp: ${BUILD_STAMP}"

# --- preconditions -----------------------------------------------------------
need_web() {
    if [ ! -f web/dist/index.html ]; then
        echo "ERROR: web/dist/index.html missing — root static.go embeds it." >&2
        echo "Build the web UI first: 'make web' (or 'make all')." >&2
        exit 1
    fi
}

# go's module/build caches live in the image's native FS (mounted volumes in the
# Makefile), never on the 9p-mounted /src — keeps builds fast and avoids 9p
# file-lock issues. GOFLAGS keeps module downloads reproducible.
export GOFLAGS="${GOFLAGS:-}"

build_service() {
    need_web
    echo "[linux] building wesync (service, CGO_ENABLED=0)"
    CGO_ENABLED=0 go build -ldflags="${LD_COMMON}" -o /out/wesync .
    echo "[linux]   -> /out/wesync"
}

build_gui() {
    need_web
    echo "[linux] building wesync-app (Wails GUI, CGO + webkit2_41)"
    CGO_ENABLED=1 go build -tags "desktop,production,webkit2_41" -o /out/wesync-app ./cmd/app
    echo "[linux]   -> /out/wesync-app"
}

# Fetch the Windows Syncthing build. It must land at dist/windows/syncthing.exe
# BEFORE wesync.exe is built, because embed_syncthing_windows.go embeds it via
# //go:embed dist/windows/syncthing.exe. /out is mounted to the same host dir as
# /src/dist/windows, so writing here makes the embed resolve.
fetch_syncthing_windows() {
    local out="/out/syncthing.exe"
    if [ -f "$out" ] && [ "${SYNCTHING_FORCE:-0}" != "1" ]; then
        echo "[win] syncthing.exe already present (set SYNCTHING_FORCE=1 to refresh)"
        return
    fi
    local name="syncthing-windows-amd64-${SYNCTHING_VERSION}"
    local url="https://github.com/syncthing/syncthing/releases/download/${SYNCTHING_VERSION}/${name}.zip"
    echo "[win] fetching syncthing ${SYNCTHING_VERSION} (windows-amd64)"
    curl -fsSL "$url" -o /tmp/st.zip
    unzip -oq /tmp/st.zip -d /tmp
    cp "/tmp/${name}/syncthing.exe" "$out"
    echo "[win]   -> /out/syncthing.exe (embedded into wesync.exe)"
}

# Full Windows build in Docker: both exes (pure-Go cross-compile, no CGO/mingw —
# Wails-on-Windows uses pure-Go go-webview2) + NSIS installer via Linux makensis.
build_windows() {
    need_web
    local ver="${WESYNC_VERSION:-0.1.0}"

    # 1. syncthing.exe first (wesync.exe embeds it — see fetch note above)
    fetch_syncthing_windows

    # 2. service exe -> repo root (wesync.nsi references ..\..\wesync.exe)
    echo "[win] building wesync.exe (service, embeds syncthing)"
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
        go build -ldflags="${LD_COMMON} -H windowsgui" -o /src/wesync.exe .

    # 3. Windows resources for the GUI exe. `go build` (unlike `wails build`)
    #    embeds NO resources, so we generate a .syso the linker picks up.
    #    CRITICAL: Wails sets the window/taskbar icon at RUNTIME by loading
    #    RT_GROUP_ICON resource ID 3 (winc.AppIconID; see Wails window.go ->
    #    NewIconFromResource(instance, 3)). `go-winres simply` names the icon group
    #    "APP" (a NAME, not numeric ID 3), so LoadIcon(MAKEINTRESOURCE(3)) returned
    #    nothing and the title bar showed the blank OS placeholder — even though the
    #    file icon looked fine. cmd/app/winres/winres.json pins the icon to #3 and
    #    carries the DPI-aware GUI manifest. All icons derive from the CURRENT
    #    master icons/web/icon-512.png.
    echo "[win] generating Windows resources (icon ID 3 + manifest)"
    convert /src/icons/web/icon-512.png -define icon:auto-resize=256,64,48,32,16 /out/wesync.ico
    # Tray icon is go:embed'd from cmd/app/build/windows/icon.ico — regenerate it
    # from the same master so the systray matches the window/installer icon.
    convert /src/icons/web/icon-512.png -define icon:auto-resize=256,64,48,32,16 /src/cmd/app/build/windows/icon.ico
    ( cd /src/cmd/app && go-winres make --in winres/winres.json --arch amd64 )

    # 4. GUI exe -> dist/windows (links rsrc_windows_amd64.syso from cmd/app)
    echo "[win] building wesync-app.exe (Wails GUI)"
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
        go build -tags "desktop,production" -ldflags="-s -w -H windowsgui" -o /out/wesync-app.exe ./cmd/app
    rm -f /src/cmd/app/rsrc_windows_*.syso

    # 5. NSIS installer via Linux makensis. The committed .nsi uses Windows
    #    backslash source paths (..\..\), which POSIX makensis treats as literals,
    #    so generate a sibling .nsi with forward slashes on JUST those source-path
    #    lines (runtime $INSTDIR\ paths stay backslash). Sibling location keeps the
    #    ../../ relative paths resolving against /src. Patch the version too.
    local gen=/src/platform/windows/wesync.gen.nsi
    sed '/\.\.\\/ s#\\#/#g' /src/platform/windows/wesync.nsi > "$gen"
    sed -i "s/!define APP_VERSION .*/!define APP_VERSION \"${ver}\"/" "$gen"
    echo "[win] makensis -> WeSync-${ver}-setup.exe"
    makensis -V2 "$gen"
    rm -f "$gen"
    echo "[win]   -> /out/WeSync-${ver}-setup.exe"
}

# Fetch the matching Syncthing release so the Linux package is self-contained
# (embed_syncthing_other.go is empty — on Linux wesync looks for 'syncthing'
# alongside it / in PATH).
fetch_syncthing() {
    local out="/out/syncthing"
    if [ -f "$out" ] && [ "${SYNCTHING_FORCE:-0}" != "1" ]; then
        echo "[linux] syncthing already present (set SYNCTHING_FORCE=1 to refresh)"
        return
    fi
    local name="syncthing-linux-${SYNCTHING_ARCH}-${SYNCTHING_VERSION}"
    local url="https://github.com/syncthing/syncthing/releases/download/${SYNCTHING_VERSION}/${name}.tar.gz"
    echo "[linux] fetching syncthing ${SYNCTHING_VERSION} (${SYNCTHING_ARCH})"
    curl -fsSL "$url" -o /tmp/st.tgz
    tar -xzf /tmp/st.tgz -C /tmp
    cp "/tmp/${name}/syncthing" "$out"
    chmod +x "$out"
    echo "[linux]   -> /out/syncthing"
}

# Roll the complete product (service + GUI + syncthing) into one self-contained
# tarball with a short run note, staged in a versioned top-level dir.
build_tarball() {
    local ver="${WESYNC_VERSION:-0.1.0}"
    local stage="wesync-${ver}-linux-${SYNCTHING_ARCH}"
    local dir="/tmp/${stage}"
    rm -rf "$dir" && mkdir -p "$dir"
    cp /out/wesync /out/wesync-app /out/syncthing "$dir/"
    cat > "$dir/README.txt" <<EOF
WeSync ${ver} for Linux (${SYNCTHING_ARCH})

Contents:
  wesync       - backend service (serves the UI on http://localhost:47820)
  wesync-app   - desktop GUI window (needs GTK3 + WebKit2GTK 4.1 + a display)
  syncthing    - sync engine, launched by wesync (must sit beside it)

Run headless (browser UI):   ./wesync
Run desktop app:             ./wesync &   then   ./wesync-app
EOF
    tar -czf "/out/${stage}.tar.gz" -C /tmp "$stage"
    rm -rf "$dir"
    echo "[linux]   -> /out/${stage}.tar.gz"
}

# Build the complete .deb + .rpm bundle with nfpm (see platform/linux/nfpm.yaml).
# Lays out /opt/wesync/{wesync,wesync-app,syncthing} + desktop entry + icon +
# systemd user unit + /usr/bin symlinks.
build_pkg() {
    local ver="${WESYNC_VERSION:-0.1.0}"
    # nfpm uses Debian arch names (amd64/arm64) — same as SYNCTHING_ARCH here.
    export WESYNC_VERSION="$ver"
    export WESYNC_ARCH="${SYNCTHING_ARCH}"
    # Binaries must be executable so the package preserves the bit.
    chmod +x /out/wesync /out/wesync-app /out/syncthing
    for fmt in deb rpm; do
        echo "[linux] nfpm: building .${fmt}"
        nfpm package -f /src/platform/linux/nfpm.yaml -p "$fmt" -t /out
    done
}

case "$TARGET" in
    service) build_service ;;
    gui)     build_gui ;;
    windows) build_windows ;;
    all)
        build_service
        build_gui
        fetch_syncthing
        ;;
    tarball)
        build_service
        build_gui
        fetch_syncthing
        build_tarball
        ;;
    pkg)
        build_service
        build_gui
        fetch_syncthing
        build_pkg
        ;;
    *)
        echo "ERROR: unknown target '$TARGET' (use: service | gui | windows | all | tarball | pkg)" >&2
        exit 1
        ;;
esac

# Drop the build stamp next to the artifacts so it's trivial to compare against
# what a device reports (Settings footer / GET /api/status).
echo "${BUILD_STAMP}" > /out/BUILD-STAMP.txt
echo "[build] THIS BUILD: ${BUILD_STAMP}   (also in /out/BUILD-STAMP.txt)"
echo "[linux] done. Artifacts in /out:"
ls -lh /out
