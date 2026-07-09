// cursor-usage prints a Cursor account's current usage/quota snapshot.
//
// Usage:
//
//	cursor-usage                        # load auth from Cursor IDE SQLite
//	cursor-usage -account /path.json    # load from a JSON account file
//	cursor-usage -format table          # human-readable output
//	cursor-usage -timeout 20s
//
// Exits with:
//
//	0  success
//	1  fatal error (no auth, all RPCs failed)
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
	"github.com/router-for-me/cursor-proto/usage"
)

func main() {
	accountPath := flag.String("account", "", "path to account JSON (default: read Cursor IDE token)")
	format := flag.String("format", "json", "output format: json | table")
	timeout := flag.Duration("timeout", 15*time.Second, "overall fetch timeout")
	flag.Parse()

	acc, err := loadAccount(*accountPath)
	if err != nil {
		fatal(err)
	}
	client := usage.New(executor.NewClient(acc))

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	snap, err := client.Fetch(ctx)
	if err != nil {
		fatal(err)
	}

	switch strings.ToLower(*format) {
	case "table":
		printTable(snap)
	default:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fatal(err)
		}
	}
}

func loadAccount(path string) (*auth.Account, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open account: %w", err)
		}
		defer f.Close()
		var a auth.Account
		if err := json.NewDecoder(f).Decode(&a); err != nil {
			return nil, fmt.Errorf("decode account: %w", err)
		}
		if a.AccessToken == "" {
			return nil, errors.New("account JSON has empty access_token")
		}
		return &a, nil
	}
	return loadAccountFromIDE()
}

func loadAccountFromIDE() (*auth.Account, error) {
	dbPath := ideStoragePath()
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open ide db: %w", err)
	}
	defer db.Close()
	var access, email string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&access); err != nil {
		return nil, fmt.Errorf("read accessToken from ide db: %w", err)
	}
	_ = db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/cachedEmail'`).Scan(&email)
	machineID, _ := auth.GetMachineID()
	macID, _ := auth.GetMacMachineID()
	return &auth.Account{
		Email:        email,
		AccessToken:  access,
		MachineID:    machineID,
		MacMachineID: macID,
	}, nil
}

func ideStoragePath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	case "linux":
		return filepath.Join(home, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User", "globalStorage", "state.vscdb")
	default:
		return filepath.Join(home, ".cursor", "state.vscdb")
	}
}

func printTable(s *usage.Snapshot) {
	line := func(label, value string) {
		fmt.Printf("  %-32s %s\n", label, value)
	}
	fmt.Println("Cursor Usage Snapshot")
	fmt.Println("---------------------")
	line("period_start", s.PeriodStart.Format(time.RFC3339))
	line("period_end", s.PeriodEnd.Format(time.RFC3339))
	line("total_spend", usage.FormatCents(s.TotalSpend))
	line("included_spend", usage.FormatCents(s.IncludedSpend))
	line("remaining", usage.FormatCents(s.Remaining))
	line("limit", usage.FormatCents(s.Limit))
	line("hard_limit", usage.FormatCents(s.HardLimit))
	line("no_usage_based_allowed", fmt.Sprintf("%v", s.NoUsageBasedAllowed))
	line("premium_requests_enabled", fmt.Sprintf("%v", s.UsageBasedPremiumRequestsEnabled))
	if s.Email != "" {
		line("email", s.Email)
	}
	if s.Country != "" {
		line("country", s.Country)
	}
	if s.SignUpType != "" {
		line("sign_up_type", s.SignUpType)
	}
	if s.CreatedAt != "" {
		line("created_at", s.CreatedAt)
	}
	fmt.Println()
	fmt.Println("Windowed spend")
	line("24h", usage.FormatCents(s.Spend24h))
	line("7d", usage.FormatCents(s.Spend7d))
	line("30d", usage.FormatCents(s.Spend30d))
	fmt.Println()
	fmt.Println("Slow pool / rate limits")
	line("in_slow_pool", fmt.Sprintf("%v", s.InSlowPool))
	if s.InSlowPool {
		line("reason", s.SlowReason)
		line("detail", s.SlowDetail)
		line("slowness_ms", fmt.Sprintf("%d", s.SlownessMs))
	}
	if s.RateLimitResetAt != nil && !s.RateLimitResetAt.IsZero() {
		line("rate_limit_reset_at", s.RateLimitResetAt.Format(time.RFC3339))
	}
	if s.RateLimitResetDaysRemaining != 0 {
		line("rate_limit_reset_days", fmt.Sprintf("%d", s.RateLimitResetDaysRemaining))
	}
	if s.LastRateLimitError != "" {
		line("last_rate_limit_error", s.LastRateLimitError)
	}
	if len(s.Errors) > 0 {
		fmt.Println()
		fmt.Println("Errors (non-fatal)")
		for k, v := range s.Errors {
			line(k, v)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cursor-usage:", err)
	os.Exit(1)
}
