// cursor-pool-summary prints CPA's Cursor pool status in a terminal
// table. It speaks HTTP to a running CPA instance and hits the plugin's
// management routes.
//
// Usage:
//
//	cursor-pool-summary                              # localhost:7999
//	cursor-pool-summary -cpa-url http://cpa:7999     # remote
//	cursor-pool-summary -token $MANAGEMENT_PASSWORD  # inline
//	cursor-pool-summary -accounts                    # per-account rows
//	cursor-pool-summary -json                        # dump raw json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const managementPath = "/v0/management/cli-proxy-api/cursor"

func main() {
	base := flag.String("cpa-url", envOr("CPA_URL", "http://127.0.0.1:7999"),
		"CPA base URL (no trailing slash)")
	token := flag.String("token", os.Getenv("MANAGEMENT_PASSWORD"),
		"CPA management password (Authorization: Bearer …)")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP timeout")
	accounts := flag.Bool("accounts", false, "also print per-account status")
	jsonOut := flag.Bool("json", false, "print the raw pool-summary JSON")
	flag.Parse()

	client := &http.Client{Timeout: *timeout}

	summary, err := fetch(client, *base+managementPath+"/pool-summary", *token)
	if err != nil {
		fatal("pool-summary: %v", err)
	}
	if *jsonOut {
		os.Stdout.Write(summary)
		fmt.Println()
		return
	}

	fmt.Println("Cursor Pool Summary")
	fmt.Println("-------------------")
	printSummary(summary)
	if !*accounts {
		return
	}

	acc, err := fetch(client, *base+managementPath+"/accounts", *token)
	if err != nil {
		fatal("accounts: %v", err)
	}
	fmt.Println()
	fmt.Println("Accounts")
	fmt.Println("--------")
	printAccounts(acc)
}

func fetch(client *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			fmt.Fprintf(os.Stderr, "warn: close body: %v\n", errClose)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printSummary(raw []byte) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		fmt.Fprintln(os.Stderr, "warn: decode summary:", err)
		fmt.Println(string(raw))
		return
	}
	rows := []struct {
		label, key string
	}{
		{"total", "total"},
		{"active", "active"},
		{"slow_pool", "slow_pool"},
		{"expiring_soon", "expiring_soon"},
	}
	for _, r := range rows {
		fmt.Printf("  %-22s %v\n", r.label, body[r.key])
	}
	if breakdown, ok := body["country_breakdown"].(map[string]any); ok {
		fmt.Printf("  %-22s %s\n", "country_breakdown", joinKV(breakdown))
	}
	if breakdown, ok := body["plan_breakdown"].(map[string]any); ok {
		fmt.Printf("  %-22s %s\n", "plan_breakdown", joinKV(breakdown))
	}
	if ts, ok := body["generated_at"].(string); ok {
		fmt.Printf("  %-22s %s\n", "generated_at", ts)
	}
}

func printAccounts(raw []byte) {
	var body struct {
		Accounts []map[string]any `json:"accounts"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		fmt.Fprintln(os.Stderr, "warn: decode accounts:", err)
		fmt.Println(string(raw))
		return
	}
	fmt.Printf("  %-28s %-10s %-8s %-12s %-8s %s\n",
		"email", "plan", "country", "remaining", "slow", "jwt_expires_at")
	for _, a := range body.Accounts {
		fmt.Printf("  %-28s %-10s %-8s %-12s %-8v %s\n",
			trunc(str(a["email"]), 28),
			trunc(str(a["plan"]), 10),
			trunc(str(a["country"]), 8),
			trunc(dollars(a["remaining_cents"]), 12),
			a["in_slow_pool"],
			str(a["jwt_expires_at"]),
		)
	}
}

func joinKV(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func dollars(v any) string {
	f, ok := v.(float64)
	if !ok {
		return "-"
	}
	cents := int64(f)
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cursor-pool-summary: "+format+"\n", args...)
	os.Exit(1)
}
