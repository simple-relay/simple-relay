package ui

import "embed"

// FS contains the embedded Web UI assets and HTML templates.
//
//go:embed all:static index.html
var FS embed.FS
