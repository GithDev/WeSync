//go:build windows

package main

import _ "embed"

// iconBytes is the WeSync system tray icon, embedded at build time.
//
//go:embed build/windows/icon.ico
var iconBytes []byte
