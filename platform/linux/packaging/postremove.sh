#!/bin/sh
# Runs on uninstall AND upgrade — only kill on a real removal so an upgrade's
# fresh start (postinstall) isn't taken down with it.
#   rpm:  $1 = 0 on uninstall, 1 on upgrade
#   deb:  $1 = "remove"/"purge" on removal, "upgrade" on upgrade
case "${1:-0}" in
    0 | remove | purge)
        # The package's files (incl. the autostart entry) are already gone; make
        # sure nothing is left running so the app fully disappears — no lingering
        # background process, the thing that makes Flatpak feel messy.
        pkill -f /opt/wesync/wesync    2>/dev/null || true
        pkill -f /opt/wesync/syncthing 2>/dev/null || true
        if command -v update-desktop-database >/dev/null 2>&1; then
            update-desktop-database -q /usr/share/applications 2>/dev/null || true
        fi
        ;;
esac
exit 0
