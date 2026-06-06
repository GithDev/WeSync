# WeSync build orchestration — one interface, Docker (podman) all the way.
#
# Run at the repo root:  make <target>
#
# Every target is independent ("build one at a time"); `make all` runs the lot.
# Toolchain lives in pinned images (platform/*/Dockerfile); the repo is mounted
# at run time and artifacts land in dist/<platform>/ — source never bakes into a
# layer. Mirrors the Android image's contract.
#
# Quick start:
#   make web            # build the React frontend once (all targets embed it)
#   make linux-service  # backend service binary  -> dist/linux/wesync
#   make linux          # service + GUI + syncthing (complete Linux product)
#   make all            # web + linux + windows + android

# ── config ──────────────────────────────────────────────────────────────────
PODMAN            ?= podman
ROOT              := $(CURDIR)
DIST              := $(ROOT)/dist
SYNCTHING_VERSION ?= v2.1.1
VERSION           ?= 0.1.0

IMAGE_LINUX   := wesync-linux
IMAGE_ANDROID := wesync-android
IMAGE_FLATPAK := wesync-flatpak
IMAGE_NODE    := docker.io/library/node:22-bookworm

# GNOME runtime/SDK cache (~1.5 GB) — persisted so it downloads only once.
FLATPAK_VOL := wesync-flatpak-data

# Persistent named volumes so rebuilds reuse the Go/npm caches (never on the
# slow 9p /src mount). Wiped by `make clean-cache`.
GOBUILD_VOL := wesync-gobuild
GOMOD_VOL   := wesync-gomod
NODE_VOL    := wesync-node-modules

GO_CACHE := -v $(GOBUILD_VOL):/root/.cache/go-build -v $(GOMOD_VOL):/go/pkg/mod

# Common run shape for the Linux toolchain image.
LINUX_RUN = $(PODMAN) run --rm \
	-v "$(ROOT):/src" -v "$(DIST)/linux:/out" $(GO_CACHE) \
	-e SYNCTHING_VERSION=$(SYNCTHING_VERSION) -e WESYNC_VERSION=$(VERSION) $(IMAGE_LINUX)

.DEFAULT_GOAL := help

# ── help ────────────────────────────────────────────────────────────────────
.PHONY: help
help:
	@echo "WeSync build targets:"
	@echo "  make web             Build the React frontend (web/dist) — run once; all targets embed it"
	@echo "  make linux-service   Backend service binary       -> dist/linux/wesync"
	@echo "  make linux-gui       Wails desktop GUI (GTK/WebKit) -> dist/linux/wesync-app"
	@echo "  make linux           Service + GUI + syncthing     -> dist/linux/   (complete Linux product)"
	@echo "  make linux-tar       Complete product as one .tar.gz -> dist/linux/wesync-<ver>-linux-amd64.tar.gz"
	@echo "  make linux-pkg       Windows-like .deb + .rpm -> dist/linux/  (autostart at login, clean uninstall)"
	@echo "  make linux-flatpak   (legacy) Flatpak bundle -> dist/linux/wesync-<ver>.flatpak"
	@echo "  make windows         Full Windows installer (svc+GUI+syncthing+NSIS) -> dist/windows/WeSync-<ver>-setup.exe"
	@echo "  make android         Debug APK                     -> dist/android/  (uses platform/android image)"
	@echo "  make all             ALL installables: web + flatpak + .deb/.rpm + Windows installer + APK"
	@echo "  make clean           Remove dist/ artifacts"
	@echo "  make clean-cache     Remove the cache volumes ($(GOBUILD_VOL), $(GOMOD_VOL), $(NODE_VOL))"

# ── web (frontend) ──────────────────────────────────────────────────────────
# Built in a node container; node_modules lives in a named volume (off 9p) so
# `npm ci` is fast on re-runs. dist is written back to the mounted web/dist.
.PHONY: web
web:
	@echo "== web (npm ci && npm run build) =="
	$(PODMAN) run --rm -w /src/web \
		-v "$(ROOT):/src" -v $(NODE_VOL):/src/web/node_modules \
		$(IMAGE_NODE) sh -c "npm ci && npm run build"

# ── images ──────────────────────────────────────────────────────────────────
.PHONY: image-linux image-android image-flatpak
image-linux:
	$(PODMAN) build -f platform/linux/Dockerfile -t $(IMAGE_LINUX) .
image-android:
	$(PODMAN) build -f platform/android/Dockerfile -t $(IMAGE_ANDROID) .
image-flatpak:
	$(PODMAN) build -f platform/linux/flatpak/Dockerfile -t $(IMAGE_FLATPAK) .

# ── linux ───────────────────────────────────────────────────────────────────
.PHONY: linux linux-service linux-gui linux-tar linux-pkg
linux-service: image-linux
	@mkdir -p "$(DIST)/linux"
	$(LINUX_RUN) service
linux-gui: image-linux
	@mkdir -p "$(DIST)/linux"
	$(LINUX_RUN) gui
linux: image-linux
	@mkdir -p "$(DIST)/linux"
	$(LINUX_RUN) all
linux-tar: image-linux
	@mkdir -p "$(DIST)/linux"
	$(LINUX_RUN) tarball
linux-pkg: image-linux
	@mkdir -p "$(DIST)/linux"
	$(LINUX_RUN) pkg

# Cross-distro native desktop build. --privileged: flatpak-builder needs bwrap
# (user namespaces). Runtime/SDK cached in $(FLATPAK_VOL). Needs the binaries,
# so it builds `linux` first.
.PHONY: linux-flatpak
linux-flatpak: linux image-flatpak
	@mkdir -p "$(DIST)/linux"
	$(PODMAN) run --rm --privileged \
		-v "$(ROOT):/src" -v "$(DIST)/linux:/out" \
		-v $(FLATPAK_VOL):/root/.local/share/flatpak \
		-e WESYNC_VERSION=$(VERSION) $(IMAGE_FLATPAK)

# ── windows (service .exe, cross-compiled from the Linux image) ──────────────
.PHONY: windows
windows: image-linux
	@mkdir -p "$(DIST)/windows"
	$(PODMAN) run --rm \
		-v "$(ROOT):/src" -v "$(DIST)/windows:/out" $(GO_CACHE) \
		$(IMAGE_LINUX) windows

# ── android (refresh embedded web, then existing android image) ──────────────
.PHONY: android
android: image-android
	@mkdir -p "$(DIST)/android"
	@echo "== refresh mobile/webdist from web/dist =="
	@rm -rf "$(ROOT)/mobile/webdist" && cp -r "$(ROOT)/web/dist" "$(ROOT)/mobile/webdist"
	$(PODMAN) run --rm \
		-v "$(ROOT):/src" -v "$(DIST)/android:/out" \
		$(IMAGE_ANDROID) assembleDebug

# ── everything ──────────────────────────────────────────────────────────────
# The full set of INSTALLABLE artifacts across platforms (not just raw binaries):
# Linux .deb/.rpm, the Windows installer, and the Android APK. web is built first
# since every target embeds it. (Flatpak is intentionally NOT here — it's the
# wrong model for a background sync service; .deb/.rpm + autostart mirrors the
# Windows install/uninstall experience. `make linux-flatpak` still exists if ever
# wanted.)
.PHONY: all
all: web linux-pkg windows android

# ── cleanup ─────────────────────────────────────────────────────────────────
.PHONY: clean clean-cache
clean:
	rm -rf "$(DIST)/linux" "$(DIST)/android"
	# dist/windows also holds syncthing.exe (slow to re-download; build.sh skips
	# it when present), the generated wesync.ico, the GUI exe and the NSIS
	# installer. Only nuke the cross-compiled service exe so the cache survives.
	rm -f "$(DIST)/windows/wesync.exe"
clean-cache:
	-$(PODMAN) volume rm $(GOBUILD_VOL) $(GOMOD_VOL) $(NODE_VOL) $(FLATPAK_VOL)
