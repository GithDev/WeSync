# WeSync Linux build toolchain.
#
# Pinned, OS-independent image that builds the Linux artifacts on a Debian
# base — identical environment locally (podman/docker) and in CI. Mirrors the
# Android image's contract: the image carries ONLY the toolchain; the repo is
# mounted at run time (see the Makefile / build.sh), so source never bakes into
# a layer and the image stays cacheable across code changes.
#
# Build:
#   podman build -f platform/linux/Dockerfile -t wesync-linux .
# Run (from repo root, web already built into web/dist):
#   podman run --rm -v "$PWD:/src" -v "$PWD/dist/linux:/out" wesync-linux all
#
# Targets (entrypoint arg): service | gui | all | windows. See build.sh.

# Fully-qualified so it resolves under podman too (no unqualified-search registries).
# Same base family as platform/android/Dockerfile -> shared base layers.
FROM docker.io/library/golang:1.26-bookworm

ENV DEBIAN_FRONTEND=noninteractive

# --- GUI toolchain (Wails: CGO + GTK3 + WebKit2GTK 4.1 + appindicator tray) ----
# The service binary is pure Go (CGO_ENABLED=0) and needs none of this, but the
# same image builds both targets, so the deps live here once. Package set matches
# what testdata/wsl-*.sh proved the Wails app links against (gtk-3, webkit2gtk-4.1)
# plus libayatana-appindicator3 for the fyne.io/systray tray.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential pkg-config \
        libgtk-3-dev \
        libwebkit2gtk-4.1-dev \
        libayatana-appindicator3-dev \
        curl ca-certificates git \
        nsis unzip imagemagick && \
    rm -rf /var/lib/apt/lists/*

# --- nfpm (builds the .deb / .rpm bundle) — pinned for reproducibility --------
# Compiled from source with the image's Go; lands on PATH at /go/bin.
RUN go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.46.3

# --- go-winres: embeds the Windows icon + DPI manifest into wesync-app.exe -----
# Plain `go build` (our cross-compile) does NOT embed resources the way
# `wails build` does, so without this the Windows GUI shows no icon. go-winres
# generates a .syso the Go linker picks up. Pinned.
RUN go install github.com/tc-hib/go-winres@v0.3.3

# UTF-8 locale (Go doc comments / generated code may carry non-ASCII); harmless
# and keeps tooling from defaulting to the POSIX C locale.
ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

WORKDIR /src
COPY platform/linux/build.sh /usr/local/bin/build.sh
# Strip any CR (the repo may be checked out with CRLF on Windows) and make exec.
RUN sed -i 's/\r$//' /usr/local/bin/build.sh && \
    chmod +x /usr/local/bin/build.sh

ENTRYPOINT ["/usr/local/bin/build.sh"]
