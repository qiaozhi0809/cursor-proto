package kernel

import (
	"encoding/base64"
	_ "embed"
)

// logoSVG is the Cursor brand mark embedded at build time from
// plugin/cursor/assets/logo.svg. The plugin exposes it as a data URI
// through the register/reconfigure metadata so CPA's management UI can
// render the plugin card and its associated cursor auth accounts
// without depending on an external image host.
//
//go:embed ../assets/logo.svg
var logoSVG []byte

// logoDataURI returns the SVG encoded as a data URI, computed once on
// first access.
var logoDataURI = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(logoSVG)
