//go:build verify

// The verify build tag drives every management route in-process
// against a fake SnapshotFetcher and emits the resulting JSON bodies
// alongside the shape of the curl command CPA operators would use.
//
// Regenerate with:
//
//	cd plugin/cursor
//	go test -tags verify -run TestGeneratePhase8dLog -v
//
// The transcript lands at phase-8d-verify.log in the repo root.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/usage"
)

func TestGeneratePhase8dLog(t *testing.T) {
	prev := globalRegistry
	defer func() { globalRegistry = prev }()

	rlReset := time.Date(2026, 7, 11, 4, 30, 0, 0, time.UTC)
	fetch := func(ctx context.Context, a *auth.Account) (*usage.Snapshot, error) {
		snap := &usage.Snapshot{
			PeriodStart:                      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			PeriodEnd:                        time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
			TotalSpend:                       1350,
			IncludedSpend:                    1000,
			Remaining:                        1650,
			Limit:                            3000,
			Spend24h:                         120,
			Spend7d:                          520,
			Spend30d:                         1350,
			RateLimitResetAt:                 &rlReset,
			RateLimitResetDaysRemaining:      1,
			HardLimit:                        50000,
			UsageBasedPremiumRequestsEnabled: true,
			Email:                            a.Email,
			SignUpType:                       "personal",
			Fetched: usage.Fetched{
				CurrentPeriodUsage: true, Me: true, HardLimit: true,
				PremiumRequests: true, SlowPoolStatus: true,
			},
		}
		if a.Email == "pool-cn@example.com" {
			snap.Country = "CN"
			snap.InSlowPool = true
			snap.SlowReason = "included spend exceeded"
		} else {
			snap.Country = "US"
		}
		return snap, nil
	}

	globalRegistry = newAuthRegistry(30 * time.Second)
	globalRegistry.SetFetcher(fetch)
	for _, email := range []string{"pool-us@example.com", "pool-cn@example.com"} {
		acc := &auth.Account{
			Email:        email,
			UserID:       "user_" + strings.Split(email, "@")[0],
			AccessToken:  makeVerifyJWT(time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)),
			RefreshToken: "rt-" + email,
			AuthID:       "auth0|user_" + strings.Split(email, "@")[0],
		}
		acc.FillSessionDefaults(time.Now())
		globalRegistry.Register(acc)
	}

	logPath := "../../phase-8d-verify.log"
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			t.Errorf("close log: %v", errClose)
		}
	}()

	fmt.Fprintf(f, "# Phase 8d verify — Cursor plugin management routes\n")
	fmt.Fprintf(f, "# Generated: %s (fake SnapshotFetcher; no live Cursor calls)\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "# Plugin driven in-process via management.handle dispatch.\n\n")

	base := "http://127.0.0.1:7999/v0/management/cli-proxy-api/cursor"

	fmt.Fprintf(f, "## management.register (called on plugin boot)\n")
	fmt.Fprintf(f, "%s\n\n", indent(managementRegisterResult()))

	type row struct {
		note   string
		method string
		path   string
		query  map[string]string
	}
	rows := []row{
		{"list every registered account", http.MethodGet, "/accounts", nil},
		{"one account's detail (US)", http.MethodGet, "/account", map[string]string{"email": "pool-us@example.com"}},
		{"one account's detail (CN, slow pool)", http.MethodGet, "/account", map[string]string{"email": "pool-cn@example.com"}},
		{"probe re-fetches and busts the cache", http.MethodPost, "/account/probe", map[string]string{"email": "pool-us@example.com"}},
		{"force JWT refresh", http.MethodPost, "/account/refresh", map[string]string{"email": "pool-us@example.com"}},
		{"aggregate summary for the dashboard", http.MethodGet, "/pool-summary", nil},
		{"missing ?email= returns 400", http.MethodGet, "/account", nil},
		{"unknown route returns 404", http.MethodDelete, "/accounts", nil},
	}

	for _, tc := range rows {
		fmt.Fprintf(f, "## %s %s — %s\n", tc.method, tc.path, tc.note)
		url := base + tc.path
		if len(tc.query) > 0 {
			parts := []string{}
			for k, v := range tc.query {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
			url += "?" + strings.Join(parts, "&")
		}
		fmt.Fprintf(f, "$ curl -sS -H 'Authorization: Bearer $MANAGEMENT_PASSWORD' \\\n")
		if tc.method == http.MethodGet {
			fmt.Fprintf(f, "       '%s'\n", url)
		} else {
			fmt.Fprintf(f, "       -X %s '%s'\n", tc.method, url)
		}
		q := map[string][]string{}
		for k, v := range tc.query {
			q[k] = []string{v}
		}
		resp := routeManagement(context.Background(), managementRequest{
			Method: tc.method,
			Path:   "/v0/management/cli-proxy-api/cursor" + tc.path,
			Query:  q,
		})
		fmt.Fprintf(f, "HTTP %d\n", resp.StatusCode)
		fmt.Fprintf(f, "%s\n\n", indent(string(resp.Body)))
	}
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		buf, err := json.MarshalIndent(v, "    ", "  ")
		if err == nil {
			s = string(buf)
		}
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func makeVerifyJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, _ := json.Marshal(struct {
		Exp int64 `json:"exp"`
	}{Exp: exp.Unix()})
	pb := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + pb + "." + sig
}
