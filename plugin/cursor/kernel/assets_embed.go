package kernel

import (
	"encoding/base64"
	_ "embed"
)

// logoPNG is the Cursor brand mark embedded at build time from
// plugin/cursor/kernel/assets/logo.png. The plugin exposes it as a
// data URI through plugin.register metadata.Logo so CPA's management
// UI can render the plugin card without depending on an external
// image host.
//
// The PNG is a 108x108 square (fits the UI's square logo slots
// without stretching), whereas the earlier SVG used a 49x56 viewBox
// that skewed inside those slots.
//
//go:embed assets/logo.png
var logoPNG []byte

// logoDataURI is the PNG encoded as a data URI, computed once at
// package init.
var logoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)
