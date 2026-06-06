package main

import (
	"context"

	"fyne.io/systray"
)

// runTray starts the system tray icon.
// Must be called before wails.Run() — runs in the background on Windows.
// onShow is called when the user clicks "Open WeSync".
// onQuit is called when the user clicks "Quit".
func runTray(ctx context.Context, onShow, onQuit func()) {
	go systray.Run(
		func() { trayReady(ctx, onShow, onQuit) },
		func() {},
	)
}

func trayReady(_ context.Context, onShow, onQuit func()) {
	if len(iconBytes) > 0 {
		systray.SetIcon(iconBytes)
	}
	systray.SetTitle("WeSync")
	systray.SetTooltip("WeSync — syncing files")

	open := systray.AddMenuItem("Open WeSync", "Open the WeSync window")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit WeSync", "Stop WeSync and exit")

	go func() {
		for {
			select {
			case <-open.ClickedCh:
				// Run the show in its own goroutine: this reader MUST return to the
				// select immediately. If WindowShow ever blocks (it dispatches to the
				// Wails main thread), a synchronous call here would wedge the reader —
				// and then systray's message-loop thread blocks on the next ClickedCh
				// delivery, so right-clicking the tray icon stops showing the menu.
				go onShow()
			case <-quit.ClickedCh:
				systray.Quit()
				onQuit()
				return
			}
		}
	}()
}
