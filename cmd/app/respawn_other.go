//go:build !windows

package main

// The supervisor works around a Windows-only Wails crash path
// (see respawn_windows.go), so elsewhere the app always runs in-process.

func isSupervisedChild() bool { return true }

func superviseChild() {}
