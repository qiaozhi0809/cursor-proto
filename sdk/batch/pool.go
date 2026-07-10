package batch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
	"github.com/router-for-me/cursor-proto/usage"
)

// PoolEntry is a per-account view built by ScanPool. It bundles the
// account, its usage snapshot (when fetched) and whatever error was
// encountered during scan/verify.
type PoolEntry struct {
	Path    string          `json:"path"`
	Account *auth.Account   `json:"account"`
	Usage   *usage.Snapshot `json:"usage,omitempty"`
	Error   string          `json:"error,omitempty"`
	// Alive means the last verify attempt (usage snapshot) succeeded.
	Alive bool `json:"alive"`
	// LastUse is populated from the storage layer when available (we use
	// the file's mtime as a proxy since the CPA plugin bumps it on every
	// successful refresh; falls back to zero when unavailable).
	LastUse time.Time `json:"last_use,omitempty"`
}

// LoadPool reads every *.json file in dir (non-recursive) and parses it
// as an auth.Account. Files that fail to parse are returned as entries
// with Error set so the caller can surface them without aborting.
func LoadPool(dir string) ([]*PoolEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*PoolEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		acc, err := auth.LoadAccount(p)
		if err != nil {
			out = append(out, &PoolEntry{Path: p, Error: err.Error()})
			continue
		}
		pe := &PoolEntry{Path: p, Account: acc}
		if info, statErr := e.Info(); statErr == nil {
			pe.LastUse = info.ModTime()
		}
		out = append(out, pe)
	}
	sort.Slice(out, func(i, j int) bool {
		return emailKey(out[i]) < emailKey(out[j])
	})
	return out, nil
}

func emailKey(pe *PoolEntry) string {
	if pe == nil || pe.Account == nil {
		return pe.Path
	}
	return strings.ToLower(pe.Account.Email)
}

// Verify calls usage.Fetch against each entry that has an account. The
// timeout applies per-account; a failed fetch is recorded on pe.Error
// and does not abort subsequent entries.
func Verify(ctx context.Context, entries []*PoolEntry, perTimeout time.Duration) {
	for _, pe := range entries {
		if pe.Account == nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, perTimeout)
		client := usage.New(executor.NewClient(pe.Account))
		snap, err := client.Fetch(cctx)
		cancel()
		if err != nil {
			pe.Error = err.Error()
			pe.Alive = false
			continue
		}
		pe.Usage = snap
		// Alive means at least the /GetMe RPC (or any RPC really) worked.
		pe.Alive = snap != nil && (snap.Fetched.Me || snap.Fetched.CurrentPeriodUsage ||
			snap.Fetched.HardLimit || snap.Fetched.SlowPoolStatus)
	}
}

// Tier heuristically classifies an account into a display bucket based
// on the fetched snapshot. Cursor doesn't expose a "tier" enum, so we
// derive one from the presence of a paid billing cycle and hard limit.
func Tier(pe *PoolEntry) string {
	if pe == nil || pe.Usage == nil {
		return "?"
	}
	u := pe.Usage
	switch {
	case u.HardLimit > 0 || u.Limit > 0:
		return "Pro"
	case u.Fetched.Me:
		return "Free"
	default:
		return "?"
	}
}

// ExpiresIn returns a compact "in X" / "expired" / "unknown" string for
// the account's access token, computed from either ExpiresAt or the
// token's JWT exp claim.
func ExpiresIn(pe *PoolEntry, now time.Time) string {
	if pe == nil || pe.Account == nil {
		return "unknown"
	}
	exp := pe.Account.ExpiresAt
	if exp.IsZero() {
		exp = auth.ExpiresAtFromJWT(pe.Account.AccessToken)
	}
	if exp.IsZero() {
		return "unknown"
	}
	if exp.Before(now) {
		return "expired"
	}
	d := exp.Sub(now)
	return humanDuration(d)
}

// LastUseString returns "never" for the zero time or a "Xd Yh ago" for
// non-zero times.
func LastUseString(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < 0 {
		return t.Format("2006-01-02")
	}
	return humanDurationAgo(d)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %02dh", days, hours)
	}
	mins := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func humanDurationAgo(d time.Duration) string {
	if d < 2*time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
}

// Notes summarises operator-visible flags for a single entry: slow pool,
// missing refresh token, etc.
func Notes(pe *PoolEntry) string {
	if pe == nil {
		return ""
	}
	var parts []string
	if pe.Account != nil {
		if !pe.Account.Refreshable && pe.Account.RefreshToken == "" {
			parts = append(parts, "no_refresh")
		}
	}
	if pe.Usage != nil {
		if pe.Usage.InSlowPool {
			parts = append(parts, "slow_pool")
		}
		if pe.Usage.NoUsageBasedAllowed {
			parts = append(parts, "no_ubp")
		}
	}
	if pe.Error != "" {
		parts = append(parts, "error")
	}
	return strings.Join(parts, ",")
}
