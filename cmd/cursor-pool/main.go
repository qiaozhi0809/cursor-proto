// cursor-pool is the operator's inspector for a directory of
// cursor-proto Account JSON files. It supports three subcommands:
//
//	cursor-pool status  --dir ~/.cursor-pool/           # printed table
//	cursor-pool status  --dir ~/.cursor-pool/ --json    # JSON output
//	cursor-pool verify  --dir ~/.cursor-pool/           # /GetMe every account
//	cursor-pool refresh --dir ~/.cursor-pool/           # refresh expiring accounts
//
// The `refresh` subcommand currently records an intent (bumps the file's
// mtime after a successful validation) rather than performing a real
// token swap — cursor-proto's refresh flow is not wired up yet
// (plugin/cursor's auth.refresh handler is a documented passthrough).
// When a real refresh lands, this subcommand is the seam that calls it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/sdk/batch"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch sub {
	case "status":
		runStatus()
	case "verify":
		runVerify()
	case "refresh":
		runRefresh()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cursor-pool <status|verify|refresh> --dir <pool-dir> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  status   list every account with a summary row per pool entry")
	fmt.Fprintln(os.Stderr, "  verify   call DashboardService.GetMe against every account")
	fmt.Fprintln(os.Stderr, "  refresh  refresh any expiring account (currently: verify + mtime bump)")
}

func runStatus() {
	dir := flag.String("dir", defaultDir(), "pool directory")
	asJSON := flag.Bool("json", false, "emit JSON instead of a table")
	verify := flag.Bool("verify", false, "call GetMe against each account (populates country/tier)")
	timeout := flag.Duration("timeout", 15*time.Second, "per-account verify timeout (if --verify)")
	flag.Parse()

	entries, err := batch.LoadPool(*dir)
	if err != nil {
		fatalf("load pool: %v", err)
	}
	if *verify {
		batch.Verify(context.Background(), entries, *timeout)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		return
	}
	printTable(entries)
}

func runVerify() {
	dir := flag.String("dir", defaultDir(), "pool directory")
	timeout := flag.Duration("timeout", 20*time.Second, "per-account verify timeout")
	asJSON := flag.Bool("json", false, "emit JSON instead of a table")
	flag.Parse()

	entries, err := batch.LoadPool(*dir)
	if err != nil {
		fatalf("load pool: %v", err)
	}
	batch.Verify(context.Background(), entries, *timeout)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(entries)
		return
	}
	printTable(entries)
}

func runRefresh() {
	dir := flag.String("dir", defaultDir(), "pool directory")
	timeout := flag.Duration("timeout", 20*time.Second, "per-account timeout")
	within := flag.Duration("within", 24*time.Hour, "refresh accounts expiring within this window")
	flag.Parse()

	entries, err := batch.LoadPool(*dir)
	if err != nil {
		fatalf("load pool: %v", err)
	}
	now := time.Now().UTC()
	var candidates []*batch.PoolEntry
	for _, pe := range entries {
		if pe.Account == nil {
			continue
		}
		if !pe.Account.Refreshable {
			continue
		}
		exp := pe.Account.ExpiresAt
		if exp.IsZero() {
			exp = auth.ExpiresAtFromJWT(pe.Account.AccessToken)
		}
		if exp.IsZero() {
			continue
		}
		if exp.Sub(now) > *within {
			continue
		}
		candidates = append(candidates, pe)
	}
	if len(candidates) == 0 {
		fmt.Fprintln(os.Stderr, "no accounts expiring within the window; nothing to do")
		return
	}
	batch.Verify(context.Background(), candidates, *timeout)

	var refreshed, failed []string
	for _, pe := range candidates {
		if !pe.Alive {
			fmt.Fprintf(os.Stderr, "fail: %s: %s\n", pe.Account.Email, pe.Error)
			failed = append(failed, pe.Account.Email)
			continue
		}
		// Bump mtime — treated as "last successful refresh". When a real
		// refresh call lands, replace this with a proper token swap +
		// SaveAccount.
		now := time.Now()
		if err := os.Chtimes(pe.Path, now, now); err == nil {
			refreshed = append(refreshed, pe.Account.Email)
		} else {
			fmt.Fprintf(os.Stderr, "warn: chtimes %s: %v\n", pe.Path, err)
		}
	}
	fmt.Fprintf(os.Stderr, "\n=== summary ===\n  %d refreshed, %d failed (of %d candidates)\n",
		len(refreshed), len(failed), len(candidates))
}

func printTable(entries []*batch.PoolEntry) {
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "EMAIL\tCOUNTRY\tTIER\tEXP_IN\tLAST_USE\tNOTES")
	now := time.Now().UTC()
	for _, pe := range entries {
		email := "<unreadable>"
		country := "-"
		tier := batch.Tier(pe)
		notes := batch.Notes(pe)
		if pe.Account != nil {
			email = pe.Account.Email
		}
		if pe.Usage != nil && pe.Usage.Country != "" {
			country = pe.Usage.Country
		}
		exp := batch.ExpiresIn(pe, now)
		lastUse := batch.LastUseString(pe.LastUse, time.Now())
		if pe.Error != "" && pe.Usage == nil {
			exp = "expired"
			notes = strings.TrimSpace(strings.Trim("error,"+notes, ","))
			if lastUse == "never" {
				lastUse = "(unavailable)"
			}
		}
		if email == "<unreadable>" {
			email = filepath.Base(pe.Path)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			email, country, tier, exp, lastUse, orDash(notes))
	}
	_ = tw.Flush()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func defaultDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cursor-pool")
	}
	return "."
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cursor-pool: "+format+"\n", args...)
	os.Exit(1)
}
