// cursor-to-cpa converts a cursor-proto account JSON (as written by
// cmd/cursor-login) into the JSON shape CLIProxyAPI (CPA) expects
// under its auths/ directory.
//
// Usage:
//
//	cursor-to-cpa -in ./account.json
//	cursor-to-cpa -in ./account.json -out /path/to/cpa/cursor-foo.json
//	cursor-to-cpa -in ./account.json -dir ~/.cli-proxy-api
//
// Behaviour:
//   - When -out is set, the output is written verbatim to that path.
//   - When -dir is set, the file is written to <dir>/cursor-<sanitized_email>.json.
//   - When neither is set, the file is written to ~/.cli-proxy-api/cursor-<sanitized_email>.json
//     (which is CPA's default auth directory).
//   - Extra operator knobs (prefix, priority, note, proxy_url,
//     excluded_models, disable_cooling, request_retry, disabled) may be
//     supplied via matching flags and are recorded in the output file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/sdk/cpaformat"
)

// commaList is a repeatable string-slice flag whose value is a
// comma-separated list on the command line.
type commaList []string

func (c *commaList) String() string { return strings.Join(*c, ",") }
func (c *commaList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*c = append(*c, p)
		}
	}
	return nil
}

func main() {
	inPath := flag.String("in", "", "path to cursor-proto account JSON (required)")
	outPath := flag.String("out", "", "explicit output file path (overrides -dir)")
	outDir := flag.String("dir", "", "CPA auths directory (default: $CLIPROXY_AUTH_DIR or ~/.cli-proxy-api)")
	prefix := flag.String("prefix", "", "model routing prefix for this account")
	proxyURL := flag.String("proxy-url", "", "per-account proxy URL")
	priority := flag.Int("priority", 0, "scheduler priority hint (higher wins)")
	note := flag.String("note", "", "human-readable note for the operator")
	disabled := flag.Bool("disabled", false, "mark the account disabled")
	disableCooling := flag.Bool("disable-cooling", false, "opt out of provider-wide cooldowns")
	requestRetry := flag.Int("request-retry", 0, "per-request retry override")
	var excluded commaList
	flag.Var(&excluded, "excluded-models", "comma-separated list of models to block for this account")
	dryRun := flag.Bool("dry-run", false, "print resulting JSON to stdout instead of writing to disk")
	stdout := flag.Bool("stdout", false, "print resulting JSON to stdout AFTER writing to disk")
	flag.Parse()

	if strings.TrimSpace(*inPath) == "" {
		fmt.Fprintln(os.Stderr, "error: -in is required")
		flag.Usage()
		os.Exit(2)
	}

	acc, err := auth.LoadAccount(*inPath)
	if err != nil {
		fatalf("load account: %v", err)
	}

	out, err := cpaformat.FromAccount(acc)
	if err != nil {
		fatalf("convert account: %v", err)
	}
	out.Prefix = strings.TrimSpace(*prefix)
	out.ProxyURL = strings.TrimSpace(*proxyURL)
	out.Priority = *priority
	out.Note = strings.TrimSpace(*note)
	out.Disabled = *disabled
	out.DisableCooling = *disableCooling
	out.RequestRetry = *requestRetry
	if len(excluded) > 0 {
		out.ExcludedModels = append(out.ExcludedModels, excluded...)
	}

	buf, err := out.Marshal()
	if err != nil {
		fatalf("marshal: %v", err)
	}

	if *dryRun {
		writeStdout(buf)
		return
	}

	path, err := resolveOutputPath(*outPath, *outDir, out)
	if err != nil {
		fatalf("resolve output path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		fatalf("write file: %v", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", path, len(buf))
	if *stdout {
		writeStdout(buf)
	}
}

func resolveOutputPath(outPath, outDir string, af *cpaformat.AuthFile) (string, error) {
	outPath = strings.TrimSpace(outPath)
	if outPath != "" {
		return outPath, nil
	}
	dir := strings.TrimSpace(outDir)
	if dir == "" {
		dir = os.Getenv(cpaformat.DefaultAuthDirEnv)
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determine home dir: %w", err)
		}
		dir = filepath.Join(home, ".cli-proxy-api")
	}
	return filepath.Join(dir, af.FileName()), nil
}

func writeStdout(buf []byte) {
	// Emit the JSON followed by a newline so the tool composes well
	// with pipelines. We use encoding/json here so an invalid buffer
	// would surface as an error rather than silently propagating.
	var probe json.RawMessage
	if err := json.Unmarshal(buf, &probe); err != nil {
		fatalf("internal: produced invalid JSON: %v", err)
	}
	if _, err := io.Copy(os.Stdout, strings.NewReader(string(buf))); err != nil {
		fatalf("write stdout: %v", err)
	}
	fmt.Println()
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cursor-to-cpa: "+format+"\n", args...)
	os.Exit(1)
}
