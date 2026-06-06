package main

import "embed"

//go:embed all:web/dist
var staticFiles embed.FS
