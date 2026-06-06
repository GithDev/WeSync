//go:build windows

package main

import _ "embed"

// syncthingBin embeds the Syncthing Windows binary.
//
// NOTE: This file lives in the module root — not in cmd/ — because Go's
// //go:embed cannot traverse upward with "..", so embed targets must be
// reachable from the same directory as the source file.
//
//go:embed dist/windows/syncthing.exe
var syncthingBin []byte
