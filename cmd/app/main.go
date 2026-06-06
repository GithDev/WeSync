package main

import (
	"context"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

const (
	backendPort = 47820
	backendAddr = "localhost:47820"
	backendURL  = "http://localhost:47820"
)

func main() {
	initAppLog()
	app := NewApp()

	// --hidden: start minimized to the tray without showing the window. The
	// autostart entry passes this so login is silent (background sync via the
	// tray); install-time launch and the shortcuts omit it, so they show the
	// window as usual. The webview still loads either way, so the backend gets
	// its foreground signal (WS connect) even when the window is hidden.
	startHidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--hidden" {
			startHidden = true
		}
	}

	runTray(
		context.Background(),
		func() { app.showWindow() },
		func() { app.quit() },
	)

	// Proxy keeps the Wails URL so bindings stay active (needed for native folder picker).
	// WebSocket URLs are fixed in wsService.ts via wails.localhost host detection.
	proxy := newProxy(app, backendURL)

	err := wails.Run(&options.App{
		Title:            "WeSync",
		Width:            1100,
		Height:           760,
		MinWidth:         480,
		MinHeight:        600,
		// We handle the close button ourselves (OnBeforeClose) so we can tell the
		// backend to go silent the instant the window hides to tray — rather than
		// HideWindowOnClose, which hides without giving us a hook.
		AssetServer: &assetserver.Options{
			Handler: proxy,
		},
		BackgroundColour: &options.RGBA{R: 248, G: 250, B: 252, A: 1},
		StartHidden:      startHidden,
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Linux: &linux.Options{
			// MUST match the installed .desktop basename (app.wesync.WeSync.desktop)
			// — on Wayland GNOME maps the window to its app icon purely by this
			// app_id (g_set_prgname). With the old "WeSync" it matched nothing, so
			// the window/taskbar showed no icon.
			ProgramName: "app.wesync.WeSync",
			Icon:        iconBytes, // PNG (see embed_other.go) — window icon on X11
		},
	})
	if err != nil {
		panic(err)
	}
}
