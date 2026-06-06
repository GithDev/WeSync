//go:build !windows

package main

import _ "embed"

// iconBytes is the WeSync tray / window icon on non-Windows platforms. It's a
// PNG — Linux systray (appindicator) and Wails' Linux window icon both expect
// PNG, whereas Windows uses the .ico in embed_windows.go.
//
//go:embed build/appicon.png
var iconBytes []byte
