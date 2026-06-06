#!/bin/sh
# Runs on install AND upgrade. Refresh caches, stop any old copy (so an upgrade
# actually takes effect), then start WeSync hidden for any logged-in user so the
# install is "live" immediately — like the Windows installer. If no graphical
# session is reachable, the /etc/xdg/autostart entry starts it at next login and
# the app-menu entry opens it on demand.

if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q /usr/share/applications 2>/dev/null || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor 2>/dev/null || true
fi

# Stop any running copy so the freshly installed binaries take over (upgrade).
# A no-op on a clean install. (pkill -f /opt/wesync/wesync also matches wesync-app.)
pkill -f /opt/wesync/wesync    2>/dev/null || true
pkill -f /opt/wesync/syncthing 2>/dev/null || true

# Best-effort immediate start for each user WITH an active graphical session —
# visible (NOT --hidden; install should pop the window, like the Windows
# installer). We're root here with no display env, so reconstruct the user's
# Wayland/X11/DBus env from their runtime dir. If this can't reach a session it's
# a no-op: the /etc/xdg/autostart entry starts it (hidden) at next login, and the
# app-menu entry opens it on demand.
for u in $(loginctl list-users --no-legend 2>/dev/null | awk '{print $2}'); do
    uid=$(id -u "$u" 2>/dev/null) || continue
    [ -S "/run/user/${uid}/bus" ] || continue   # only users with a live session
    runuser -u "$u" -- env \
        XDG_RUNTIME_DIR="/run/user/${uid}" \
        DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/${uid}/bus" \
        WAYLAND_DISPLAY="wayland-0" \
        DISPLAY=":0" \
        /opt/wesync/wesync-app >/dev/null 2>&1 &
done

exit 0
