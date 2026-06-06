//go:build !windows

package main

func runAsService(run func()) { run() }
func isWindowsService() bool  { return false }
