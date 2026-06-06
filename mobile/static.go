package mobile

import (
	"embed"
	"io/fs"
)

// The React UI is embedded directly into the AAR so the WebView can load
// it from our Go HTTP server on 127.0.0.1. Go's embed rule requires files
// to live under the package directory, so the build step `cp -r web/dist
// mobile/webdist` copies them in before `gomobile bind`.
//
// If you ever see "panic: nil ioFS" coming from spaHandler, that copy
// step was missed and the embed picked up an empty tree.
//
//go:embed all:webdist
var webDistEmbedded embed.FS

func staticFS() fs.FS {
	sub, err := fs.Sub(webDistEmbedded, "webdist")
	if err != nil {
		return nil
	}
	return sub
}
