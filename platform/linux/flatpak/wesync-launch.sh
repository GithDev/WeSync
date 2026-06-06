#!/bin/sh
# Entry point inside the flatpak (manifest `command:`).
#
# Model A autostart: the BACKEND runs from login (syncing happens in the
# background, no window, works on every distro incl. tray-less GNOME). The GUI
# window is launched on demand from the app menu — this wrapper.
#
# On every launch we:
#   1. ensure the headless backend is running (single-instance lock makes a
#      second start a no-op, so this never double-starts it),
#   2. ensure the login autostart entry exists (writes to the real
#      ~/.config/autostart — reachable because we hold --filesystem=home),
#   3. open the GUI window, which just proxies to the backend on :47820.
set -eu

APP_ID=app.wesync.WeSync

# 1. backend (no-op if already running — port-scoped single-instance lock)
/app/bin/wesync >/dev/null 2>&1 &

# 2. autostart the headless backend at login (idempotent)
AUTOSTART_DIR="$HOME/.config/autostart"
AUTOSTART_FILE="$AUTOSTART_DIR/$APP_ID.desktop"
if [ ! -f "$AUTOSTART_FILE" ]; then
    mkdir -p "$AUTOSTART_DIR"
    cat > "$AUTOSTART_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=WeSync (background sync)
Comment=Keeps your folders syncing in the background
Exec=flatpak run --command=wesync $APP_ID
Icon=$APP_ID
Terminal=false
NoDisplay=true
X-Flatpak=$APP_ID
X-GNOME-Autostart-enabled=true
EOF
fi

# 3. the window (thin webview shell over the backend)
exec /app/bin/wesync-app
