package web

import "embed"

// FS embeds the index.html and static asset files for the embedded dashboard.
//go:embed index.html
var FS embed.FS
