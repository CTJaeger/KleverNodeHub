package web

import "embed"

// StaticFS embeds all static assets (CSS, JS, images) into the binary.
//
//go:embed static templates
var StaticFS embed.FS
