#!/usr/bin/env bash
# In-container flatpak build. Repo mounted at /src, output at /out, flatpak data
# (runtimes/SDK) cached in a volume at /root/.local/share/flatpak.
#
# Packages the prebuilt /out/{wesync,wesync-app,syncthing} into a single-file
# flatpak bundle: /out/wesync-<ver>.flatpak. Install per-user with:
#   flatpak install --user wesync-<ver>.flatpak
set -euo pipefail

VER="${WESYNC_VERSION:-0.1.0}"
APP_ID=app.wesync.WeSync
FP_DIR=/src/platform/linux/flatpak

cd /src

# --- preconditions: the binaries must already be built (make linux) ----------
for b in wesync wesync-app syncthing; do
    if [ ! -f "/out/$b" ]; then
        echo "ERROR: /out/$b missing — run 'make linux' first." >&2
        exit 1
    fi
done

# --- flathub remote (user) so flatpak-builder can pull the GNOME runtime ------
flatpak remote-add --user --if-not-exists \
    flathub https://dl.flathub.org/repo/flathub.flatpakrepo

# --- stage: manifest + scripts + prebuilt binaries in one build dir -----------
# flatpak-builder resolves `path:` sources relative to the manifest's dir, so
# everything the manifest references must sit beside it.
STAGE=/tmp/fp-stage
rm -rf "$STAGE"; mkdir -p "$STAGE"
cp "$FP_DIR/$APP_ID.yml"       "$STAGE/"
cp "$FP_DIR/$APP_ID.desktop"   "$STAGE/"
cp "$FP_DIR/wesync-launch.sh"  "$STAGE/wesync-launch"
cp /src/icons/web/icon-512.png "$STAGE/$APP_ID.png"
cp /out/wesync /out/wesync-app /out/syncthing "$STAGE/"

cd "$STAGE"
echo "[flatpak] flatpak-builder (runtime org.gnome.Platform//47) ..."
flatpak-builder --user --force-clean --disable-rofiles-fuse \
    --install-deps-from=flathub \
    --repo=/tmp/fp-repo build "$APP_ID.yml"

echo "[flatpak] build-bundle -> /out/wesync-${VER}.flatpak"
flatpak build-bundle /tmp/fp-repo "/out/wesync-${VER}.flatpak" "$APP_ID"

echo "[flatpak] done:"
ls -lh "/out/wesync-${VER}.flatpak"
