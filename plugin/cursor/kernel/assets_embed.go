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
// The PNG is a 180x180 square with the Cursor mark centred and
// 20% transparent padding on every side, so it drops into the UI's
// square logo slots without stretching or crowding the container
// edges. Fits neatly under CSS backgrounds that draw their own
// rounded chip / border.
//
//go:embed assets/logo.png
var logoPNG []byte

// logoDataURI is the PNG encoded as a data URI, computed once at
// package init.
var logoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)
