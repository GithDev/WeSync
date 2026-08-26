package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// go-webview2 turns any error from the resize path into os.Exit(1), which killed
// the app on transient PutBounds failures (resume, display change, DPI change)
// and left a stale tray icon behind. third_party/go-webview2 patches those two
// call sites to log instead, wired in via a replace directive in go.mod.
//
// A dependency bump that drops the replace, or a re-vendor that loses the patch,
// would silently reintroduce the crash — the app would still build and every
// other test would still pass. These tests fail loudly instead.

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // cmd/app
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

func TestGoWebview2ReplaceDirectivePresent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "github.com/wailsapp/go-webview2 => ./third_party/go-webview2") {
		t.Error("go.mod no longer replaces go-webview2 with the patched copy — " +
			"a failed resize will call os.Exit(1) and kill the app again")
	}
}

func TestResizePathDoesNotExitTheProcess(t *testing.T) {
	root := repoRoot(t)
	for _, f := range []struct{ path, fn string }{
		{filepath.Join(root, "third_party", "go-webview2", "pkg", "edge", "chromium_amd64.go"), "SetSize"},
		{filepath.Join(root, "third_party", "go-webview2", "pkg", "edge", "chromium.go"), "Resize"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Errorf("%s: %v (patched copy missing?)", f.path, err)
			continue
		}
		body, ok := funcBody(string(b), f.fn)
		if !ok {
			t.Errorf("%s: could not find func %s", f.path, f.fn)
			continue
		}
		// errorCallback is the one that ends in os.Exit(1); globalErrorCallback
		// only reports.
		if strings.Contains(body, "e.errorCallback(") {
			t.Errorf("%s: %s still calls errorCallback, which exits the process", f.path, f.fn)
		}
	}
}

// funcBody returns the source of the named top-level method, up to the closing
// brace at column 0.
func funcBody(src, name string) (string, bool) {
	i := strings.Index(src, ") "+name+"(")
	if i < 0 {
		return "", false
	}
	j := strings.Index(src[i:], "\n}\n")
	if j < 0 {
		return "", false
	}
	return src[i : i+j], true
}
