package web

import "embed"

// WebFS embeds the simulator HTML and assets.
//
//go:embed index.html
var WebFS embed.FS
