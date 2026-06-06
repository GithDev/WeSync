package stmanager

// DataDirOverride is set by mobile hosts whose filesystem has no useful
// HOME / XDG concept (notably Android, where ~/ resolves to /sdcard which
// the app cannot write to). Empty string keeps the platform default.
//
// Lives in a non-tagged file so callers can set it regardless of build
// target — the platform-specific DataDir() consults it first.
var DataDirOverride string
