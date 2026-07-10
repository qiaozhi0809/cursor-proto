// cursor-from-cpa reverses cursor-to-cpa: it reads CPA-shape auth JSON
// files and writes cursor-proto Account JSON files. Both single-file
// and directory modes are supported.
//
// Usage:
//
//	cursor-from-cpa --in ~/.cpa-pool/cursor-alice.json --out ~/.cursor-pool/
//	cursor-from-cpa --dir-in ~/.cpa-pool/ --out ~/.cursor-pool/
//
// Behaviour:
//   - Non-cursor CPA files (type != "cursor") are silently ignored in
//     batch mode so a shared CPA auths/ directory can be pointed at.
//   - Existing files under --out are overwritten (they're just token
//     material — the round-trip test relies on byte-for-byte matching).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/sdk/cpaformat"
)

func main() {
	inPath := flag.String("in", "", "path to a single CPA cursor auth JSON")
	inDir := flag.String("dir-in", "", "batch mode: directory of CPA cursor auth JSON files")
	outDir := flag.String("out", defaultOutDir(), "directory to write cursor-proto Account JSON files")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fatalf("create out dir: %v", err)
	}

	switch {
	case strings.TrimSpace(*inDir) != "":
		runBatch(*inDir, *outDir)
	case strings.TrimSpace(*inPath) != "":
		runOne(*inPath, *outDir)
	default:
		fmt.Fprintln(os.Stderr, "error: --in or --dir-in is required")
		flag.Usage()
		os.Exit(2)
	}
}

func runOne(path, outDir string) {
	if err := convertFile(path, outDir); err != nil {
		fatalf("%v", err)
	}
}

func runBatch(inDir, outDir string) {
	entries, err := os.ReadDir(inDir)
	if err != nil {
		fatalf("read dir: %v", err)
	}
	var (
		saved   []string
		skipped []string
		failed  []string
	)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(inDir, e.Name())
		if err := convertFile(p, outDir); err != nil {
			if _, isSkip := err.(skipError); isSkip {
				fmt.Fprintf(os.Stderr, "skip: %s (%v)\n", p, err)
				skipped = append(skipped, e.Name())
				continue
			}
			fmt.Fprintf(os.Stderr, "fail: %s: %v\n", p, err)
			failed = append(failed, e.Name())
			continue
		}
		saved = append(saved, e.Name())
	}
	fmt.Fprintf(os.Stderr, "\n=== summary ===\n  %d converted, %d skipped, %d failed\n",
		len(saved), len(skipped), len(failed))
	if len(failed) > 0 {
		os.Exit(1)
	}
}

func convertFile(path, outDir string) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	af, err := cpaformat.Unmarshal(buf)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if af.Type != cpaformat.ProviderType {
		return skipError{msg: fmt.Sprintf("type=%q, want %q", af.Type, cpaformat.ProviderType)}
	}
	if err := af.Validate(); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	acc, err := af.ToAccount()
	if err != nil {
		return fmt.Errorf("convert: %w", err)
	}
	acc.Refreshable = acc.RefreshToken != ""
	out, err := auth.SaveAccount(outDir, acc)
	if err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Fprintf(os.Stderr, "ok:   %s -> %s\n", path, out)
	return nil
}

type skipError struct{ msg string }

func (e skipError) Error() string { return e.msg }

func defaultOutDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cursor-pool")
	}
	return "."
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cursor-from-cpa: "+format+"\n", args...)
	os.Exit(1)
}
