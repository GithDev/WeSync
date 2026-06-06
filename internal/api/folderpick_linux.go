//go:build linux

package api

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

func pickFolder() (string, error) {
	// Native desktop dialogs first — fast path on a normal (non-sandboxed) install.
	for _, args := range [][]string{
		{"zenity", "--file-selection", "--directory", "--title=Select folder to share via WeSync"},
		{"kdialog", "--getexistingdirectory", "/", "--title", "Select folder to share via WeSync"},
	} {
		out, err := exec.Command(args[0], args[1:]...).Output()
		if err != nil {
			// Exit code 1 = user cancelled → empty path, no error (so the frontend
			// doesn't pop the manual-input modal). Anything else (binary missing,
			// crashed) means this dialog isn't usable here — try the next option.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return "", nil
			}
			continue
		}
		return strings.TrimSpace(string(out)), nil
	}
	// XDG Desktop Portal — the sandbox-correct path. Flatpak ships no zenity/kdialog,
	// but org.freedesktop.portal.FileChooser is always reachable inside the sandbox
	// (and on most modern desktops), so this is what makes the native picker work in
	// the Flatpak build instead of falling through to the manual path input.
	return pickFolderPortal()
}

// pickFolderPortal drives org.freedesktop.portal.FileChooser.OpenFile in
// directory mode over D-Bus. Returns the chosen path, or "" on cancel.
func pickFolderPortal() (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", fmt.Errorf("folder picker: no zenity/kdialog and no session bus: %w", err)
	}

	// Predict the Request object path and subscribe BEFORE the call so a fast
	// Response can't race ahead. The portal spec fixes the path as:
	//   /org/freedesktop/portal/desktop/request/<SENDER>/<TOKEN>
	// SENDER = our unique bus name without the leading ':' and with '.'->'_'.
	token := fmt.Sprintf("wesync%d", time.Now().UnixNano())
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	reqPath := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)

	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(reqPath),
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	}
	if err := conn.AddMatchSignal(match...); err != nil {
		return "", fmt.Errorf("folder picker: portal subscribe: %w", err)
	}
	defer conn.RemoveMatchSignal(match...) //nolint:errcheck

	sigCh := make(chan *dbus.Signal, 1)
	conn.Signal(sigCh)
	defer conn.RemoveSignal(sigCh)

	obj := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"directory":    dbus.MakeVariant(true),
		"modal":        dbus.MakeVariant(true),
	}
	call := obj.Call("org.freedesktop.portal.FileChooser.OpenFile", 0,
		"", "Select folder to share via WeSync", opts)
	if call.Err != nil {
		return "", fmt.Errorf("folder picker: portal call: %w", call.Err)
	}

	for {
		select {
		case sig := <-sigCh:
			if sig.Path != reqPath || len(sig.Body) < 2 {
				continue
			}
			code, _ := sig.Body[0].(uint32)
			if code != 0 {
				return "", nil // 1 = cancelled, 2 = other — treat as cancel
			}
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			uris, _ := results["uris"].Value().([]string)
			if len(uris) == 0 {
				return "", nil
			}
			u, err := url.Parse(uris[0]) // file:///home/x -> /home/x
			if err != nil {
				return "", fmt.Errorf("folder picker: bad uri %q: %w", uris[0], err)
			}
			return u.Path, nil
		case <-time.After(5 * time.Minute):
			return "", fmt.Errorf("folder picker: portal timed out")
		}
	}
}
