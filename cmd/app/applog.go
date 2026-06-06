package main

import (
	"log"
	"os"
	"path/filepath"
)

// initAppLog redirects the app's log output to a file next to the exe.
// Called from main() before anything else so all log.Printf calls go to disk.
func initAppLog() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	logDir := filepath.Join(filepath.Dir(exe), "data")
	os.MkdirAll(logDir, 0700) //nolint:errcheck
	f, err := os.OpenFile(filepath.Join(logDir, "wesync-app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Printf("wesync-app started")
}
