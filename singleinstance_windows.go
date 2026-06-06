//go:build windows

package main

import (
	"fmt"
	"log"

	"golang.org/x/sys/windows"
)

// acquireSingleInstanceLock uses a named mutex to ensure only one WeSync
// backend runs per port. Multiple instances on different ports are allowed
// (e.g. dev setup with 3 instances). Returns false if another instance is
// already running on the same port.
func acquireSingleInstanceLock(port int) (acquired bool, release func()) {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\WeSync-Backend-%d", port))
	handle, err := windows.CreateMutex(nil, true, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		log.Printf("WeSync already running — exiting duplicate instance")
		return false, nil
	}
	if err != nil {
		// Can't create mutex — allow startup anyway.
		log.Printf("WARNING: could not create single-instance mutex: %v", err)
		return true, func() {}
	}
	return true, func() { windows.CloseHandle(handle) } //nolint:errcheck
}
