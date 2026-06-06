//go:build !windows

package main

// syncthingBin is populated on Windows only.
// On other platforms, place syncthing alongside wesync.
var syncthingBin []byte
