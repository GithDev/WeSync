//go:build windows
// +build windows

package edge

import (
	"github.com/wailsapp/go-webview2/internal/w32"
)

// WESYNC PATCH: a failed PutBounds no longer terminates the process.
//
// errorCallback ends in os.Exit(1). PutBounds fails transiently when the
// controller is momentarily invalid — resume from sleep, a display being
// attached or removed, a DPI change — and killing the app there is what left
// users with a dead process behind a stale tray icon.
//
// Dropping the resize is safe and matches this function's own semantics: it
// already returns silently when the controller is nil. The next WM_SIZE or
// repaint re-applies the bounds.
func (e *Chromium) SetSize(bounds w32.Rect) {
	if e.controller == nil {
		return
	}

	if err := e.controller.PutBounds(bounds); err != nil {
		e.globalErrorCallback(err)
	}
}
