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
// The PNG is 114x114 with only ~6% transparent margin around the
// Cursor mark, matching the visual weight of the SVG logos the CPA
// management UI ships for its built-in providers (codex/claude/
// gemini/… each fill their container ~94%). Earlier revisions used
// heavier padding (60% content) which rendered the mark noticeably
// smaller than sibling providers.
//
//go:embed assets/logo.png
var logoPNG []byte

// logoDataURI is the PNG encoded as a data URI, computed once at
// package init.
var logoDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)
