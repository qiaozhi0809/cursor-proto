package executor

import (
	"runtime"
	"strings"
	"time"
)

// clientArch returns arch label matching Node's process.arch.
func clientArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "ia32"
	default:
		return runtime.GOARCH
	}
}

// osVersion returns a human-ish kernel version string.
// Node.js os.release() returns raw kernel version, e.g. "24.6.0" on macOS.
// We do not actually shell out — the header value only needs to be non-empty
// and plausible. IDE captures show "24.6.0" (darwin), "10.0.22631" (windows).
func osVersion() string {
	// Sensible defaults per GOOS. Callers can override by setting the header
	// again after ApplyCommonHeaders.
	switch runtime.GOOS {
	case "darwin":
		return "24.6.0"
	case "linux":
		return "6.6.0"
	case "windows":
		return "10.0.22631"
	default:
		return "unknown"
	}
}

// timezone returns the IANA name for the local zone.
func timezone() string {
	name, _ := time.Now().Zone()
	// time.Zone() returns short names ("CST"). We want IANA. Fallback to Asia/Shanghai
	// which is the capture value; callers can override.
	if strings.Contains(name, "/") {
		return name
	}
	if tz := time.Local.String(); tz != "" && strings.Contains(tz, "/") {
		return tz
	}
	return "Asia/Shanghai"
}
