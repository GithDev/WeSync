package api

import "log"

// DebugWire enables verbose logging of all wire trust events. Off by default;
// the backend flips it on when started with --debug.
var DebugWire = false

func dbg(format string, args ...any) {
	if DebugWire {
		log.Printf("[wire] "+format, args...)
	}
}
