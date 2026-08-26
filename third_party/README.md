# third_party

Vendored dependencies carrying local patches, wired in through `replace`
directives in the root `go.mod`.

## go-webview2

Upstream: https://github.com/wailsapp/go-webview2 (v1.0.22, the version Wails
v2.15.0 depends on)

**Patch:** the window-resize path no longer terminates the process.

`Chromium.errorCallback` ends in `os.Exit(1)`. Every error in the package routes
through it, including `PutBounds` failures from `SetSize`, which happen
transiently whenever the WebView2 controller is momentarily invalid — resuming
from sleep, attaching or removing a display, a DPI change.

The effect was a hard kill in the middle of a `WM_SIZE` message. Because
`os.Exit` skips all cleanup, the systray icon was never removed: the process was
gone but still looked alive in the tray, clicking it did nothing, and the taskbar
button had no window left to open a menu for. This is the crash recorded in
`wesync-app.log`, always hours after startup and consistently around sleep/resume.

Patched call sites (both marked `WESYNC PATCH`):

- `pkg/edge/chromium_amd64.go` — `SetSize`
- `pkg/edge/chromium.go` — `Resize`

Both now report through `globalErrorCallback` and return. Dropping a resize is
safe and matches the function's own semantics — `SetSize` already returns
silently when the controller is nil — and the next `WM_SIZE` or repaint
re-applies the bounds.

The patch is deliberately narrow. Initialisation failures still exit, because an
app with no working WebView2 has nothing to show.

Wails never calls `SetErrorCallback`, so this cannot be overridden from
application code.

### Upstream status

Wails fixed this in [PR #5597](https://github.com/wailsapp/wails/pull/5597)
("never treat a failed SetSize/PutBounds as fatal"), merged 2026-06-14, with the
same reasoning and the same change. It is one of a series — #5453, #5568, #5572
applied the pattern to `Eval` and `Focus`.

That PR only touched `webview2/pkg/edge/` inside the wails repo, which is the v3
layout. Wails v2 still depends on the standalone `github.com/wailsapp/go-webview2`
module, which never received the fix: v1.0.23 still calls `os.Exit(1)`. v2.14.0
and v2.15.0 both shipped after the fix without it.

So the patch here is Wails' own fix, applied to the branch it was not ported to.

**Remove this vendored copy when either happens:**

- the fix is backported to `go-webview2` (check `SetSize` in a newer release), or
- WeSync moves to Wails v3, where it is already in — v3 is still in beta
  (v3.0.0-beta.14 as of 2026-08-26), which is why WeSync is on v2.

`cmd/app/webview2_patch_test.go` fails if the `replace` directive or either patch
goes missing, so a dependency bump cannot silently reintroduce the crash.

### Updating

Re-copy the upstream module, re-apply both `WESYNC PATCH` hunks, and run
`go test ./cmd/app/`.
