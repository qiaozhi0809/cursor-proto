// cursor-login-batch runs the Cursor OAuth device-flow for many
// accounts back-to-back. For each email it prints the OAuth URL, waits
// for the user to authorise in a browser, then saves the resulting
// tokens under <out>/cursor-<sanitized_email>.json.
//
// Usage:
//
//	cursor-login-batch --emails a@icloud.com,b@gmail.com --out ~/.cursor-pool/
//	cursor-login-batch --emails-file emails.txt --out ~/.cursor-pool/
//	cursor-login-batch --emails a@example.com --force --no-browser
//
// Behaviour:
//   - Skips accounts whose file already exists on disk (unless --force).
//   - Continues on failure — the whole batch is only aborted on ctrl-C.
//   - Prints a final summary of successes, skips, and failures.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/sdk/batch"
)

func main() {
	emailsFlag := flag.String("emails", "", "comma-separated list of emails to log in")
	emailsFile := flag.String("emails-file", "", "path to a text file with one email per line")
	outDir := flag.String("out", defaultOutDir(), "directory to write account JSON files into")
	force := flag.Bool("force", false, "overwrite existing account files")
	noBrowser := flag.Bool("no-browser", false, "print the OAuth URL only, do not try to open a browser")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-account login timeout")
	interval := flag.Duration("interval", 3*time.Second, "poll interval")
	flag.Parse()

	emails, err := gatherEmails(*emailsFlag, *emailsFile)
	if err != nil {
		fatalf("emails: %v", err)
	}
	if len(emails) == 0 {
		fatalf("no emails to log in (use --emails or --emails-file)")
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fatalf("create out dir: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var (
		success []string
		skipped []string
		failed  []string
	)

	for i, email := range emails {
		fmt.Fprintf(os.Stderr, "\n[%d/%d] %s\n", i+1, len(emails), email)
		if ctx.Err() != nil {
			failed = append(failed, email+" (cancelled)")
			continue
		}
		outPath := auth.AccountFilePath(*outDir, email)
		if !*force {
			if _, err := os.Stat(outPath); err == nil {
				fmt.Fprintf(os.Stderr, "  skip: %s already exists (use --force to overwrite)\n", outPath)
				skipped = append(skipped, email)
				continue
			}
		}

		if err := loginOne(ctx, email, outPath, *outDir, *noBrowser, *interval, *timeout); err != nil {
			fmt.Fprintf(os.Stderr, "  fail: %v\n", err)
			failed = append(failed, email)
			continue
		}
		success = append(success, email)
	}

	summarise(len(emails), success, skipped, failed)
	if len(failed) > 0 && !errors.Is(ctx.Err(), context.Canceled) {
		os.Exit(1)
	}
}

func loginOne(ctx context.Context, email, outPath, outDir string, noBrowser bool, interval, timeout time.Duration) error {
	sess, err := auth.StartLogin()
	if err != nil {
		return fmt.Errorf("start login: %w", err)
	}
	// Test hook: CURSOR_POLL_URL_OVERRIDE lets integration tests drive
	// this binary against a local mock instead of api2.cursor.sh.
	if v := strings.TrimSpace(os.Getenv("CURSOR_POLL_URL_OVERRIDE")); v != "" {
		sess.PollURLBase = v
	}
	fmt.Fprintln(os.Stderr, "  Open this URL to authorise:")
	fmt.Fprintln(os.Stderr, "   ", sess.LoginURL)
	if !noBrowser {
		if openErr := openURL(sess.LoginURL); openErr != nil {
			fmt.Fprintf(os.Stderr, "  (couldn't open browser: %v)\n", openErr)
		}
	}

	result, err := sess.WaitForLogin(ctx, interval, timeout)
	if err != nil {
		return fmt.Errorf("wait for login: %w", err)
	}
	acc, err := auth.NewAccountFromPoll(result, email)
	if err != nil {
		return fmt.Errorf("build account: %w", err)
	}
	// Enrich with JWT-derived timestamps if the returned token happens to be a JWT.
	if iat := auth.IssuedAtFromJWT(acc.AccessToken); !iat.IsZero() {
		acc.IssuedAt = iat
	}
	if exp := auth.ExpiresAtFromJWT(acc.AccessToken); !exp.IsZero() {
		acc.ExpiresAt = exp
	}
	saved, err := auth.SaveAccount(outDir, acc)
	if err != nil {
		return fmt.Errorf("save account: %w", err)
	}
	// Verify we wrote where we expected (guards against subtle sanitizer drift).
	if saved != outPath {
		fmt.Fprintf(os.Stderr, "  note: saved to %s (expected %s)\n", saved, outPath)
	}
	fmt.Fprintf(os.Stderr, "  ok: %s\n", saved)
	return nil
}

func gatherEmails(inline, filePath string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(email string) {
		e := strings.TrimSpace(email)
		if e == "" {
			return
		}
		lower := strings.ToLower(e)
		if _, ok := seen[lower]; ok {
			return
		}
		seen[lower] = struct{}{}
		out = append(out, e)
	}
	for _, part := range strings.Split(inline, ",") {
		add(part)
	}
	if strings.TrimSpace(filePath) != "" {
		rows, err := batch.ReadEmailsFile(filePath)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r)
		}
	}
	return out, nil
}

func summarise(total int, success, skipped, failed []string) {
	fmt.Fprintf(os.Stderr, "\n=== summary ===\n")
	fmt.Fprintf(os.Stderr, "  %d/%d succeeded, %d skipped, %d failed\n",
		len(success), total, len(skipped), len(failed))
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "  failed: %s\n", strings.Join(failed, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "  skipped: %s\n", strings.Join(skipped, ", "))
	}
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func defaultOutDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cursor-pool")
	}
	return "."
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cursor-login-batch: "+format+"\n", args...)
	os.Exit(2)
}
