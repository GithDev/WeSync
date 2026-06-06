// wesync-app: opens the WeSync UI as a standalone app window.
//
// Finds Edge or Chrome and launches it with:
//   --app=http://localhost:47820        â†’ no browser chrome (address bar, tabs)
//   --allow-insecure-localhost          â†’ trusts our self-signed cert on localhost
//
// The result feels like a native app: own window, own taskbar entry, no cert warnings.
// Pinned to Start Menu by the installer.
package main

import (
	"os"
	"os/exec"
)

const wesyncURL = "http://localhost:47820"

var browserCandidates = []string{
	// Microsoft Edge (ships with Windows 10/11)
	`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	// Google Chrome
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
}

func main() {
	for _, browser := range browserCandidates {
		if _, err := os.Stat(browser); err == nil {
			exec.Command(browser,
				"--app="+wesyncURL,
				"--allow-insecure-localhost",
			).Start() //nolint:errcheck
			return
		}
	}
	// Fallback: open in default browser (cert warning may appear)
	exec.Command("cmd", "/c", "start", wesyncURL).Start() //nolint:errcheck
}
